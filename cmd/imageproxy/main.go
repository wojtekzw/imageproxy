// Copyright 2013 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// imageproxy starts an HTTP server that proxies requests for remote images.
package main

import (
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"strings"

	"github.com/wojtekzw/httpcache"
	"github.com/wojtekzw/httpcache/diskcache"
	"github.com/wojtekzw/diskv"
	"github.com/wojtekzw/imageproxy"
	"sourcegraph.com/sourcegraph/s3cache"
	"github.com/wojtekzw/statsd"
	"runtime/debug"
	"time"
	"os"
	"path/filepath"
)

// goxc values
var (
	// Version is the version string for imageproxy.
	Version = "HEAD"

	// BuildDate is the timestamp of when imageproxy was built.
	BuildDate string

	// GitHash - gist hash of current commit
	GitHash string
)

var addr = flag.String("addr", "localhost:8080", "TCP address to listen on")
var whitelist = flag.String("whitelist", "", "comma separated list of allowed remote hosts")
var referrers = flag.String("referrers", "", "comma separated list of allowed referring hosts")
var baseURL = flag.String("baseURL", "", "default base URL for relative remote URLs")
var cache = flag.String("cache", "", "location to cache images (see https://github.com/wojtekzw/imageproxy#cache)")
var cacheDir = flag.String("cacheDir", "", "(Deprecated; use 'cache' instead) directory to use for file cache")
var cacheSize = flag.Uint64("cacheSize", 0, "Deprecated: this flag does nothing")
var responseSize = flag.Uint64("responseSize", imageproxy.MaxRespBodySize, "Max size of original proxied request")
var signatureKey = flag.String("signatureKey", "", "HMAC key used in calculating request signatures")
var scaleUp = flag.Bool("scaleUp", false, "allow images to scale beyond their original dimensions")
var maxScaleUp = flag.Float64("maxScaleUp", imageproxy.MaxScaleUp, "limit scaleUp to maxScaleUp times (eg. 4.0 means 100x100 can be resized do 200x200 or 300x133 etc.)")
var version = flag.Bool("version", false, "print version information")
var statsdAddr = flag.String("statsdAddr", ":8125", "UDP address of Statsd compatible server")
var statsdPrefix = flag.String("statsdPrefix", "imageproxy", "prefix of Statsd data names")
var httpProxy = flag.String("httpProxy", "", "HTTP_PROXY URL to be used")


func main() {
	flag.Parse()

	if *version {
		fmt.Printf("Version: %v\nBuild: %v\nGitHash: %v\n", Version, BuildDate,GitHash)
		return
	}

	c, err := parseCache()
	if err != nil {
		log.Fatal(err)
	}

	imageproxy.Statsd, err = parseStatsd()
	if err != nil {
		log.Fatal(err)
	}

	imageproxy.Statsd.Increment("exec.started")
	proxyUrl, err := url.Parse(*httpProxy)
	if err == nil {
		os.Setenv("HTTP_PROXY", proxyUrl.String())
	}

	imageproxy.DebugFile, err = parseDebug()
	if err != nil {
		log.Fatal(err)
	}
	defer imageproxy.DebugFile.Close()
	imageproxy.DebugFile.WriteString("# " + time.Now().Format(imageproxy.DateFormat) + " starting imageproxy\n")
	imageproxy.DebugFile.Sync()


	if *responseSize == 0 {
		*responseSize = imageproxy.MaxRespBodySize
		log.Printf("Set responseSize to %d", *responseSize)
	}

	if *maxScaleUp <= 0 {
		// do nothing - leave default imageproxy.MaxScaleUp. Inform user
		log.Printf("Set maxScaleUp to %.1f", imageproxy.MaxScaleUp)
	} else {
		imageproxy.MaxScaleUp = *maxScaleUp
	}

	p := imageproxy.NewProxy(nil, c, *responseSize)
	if *whitelist != "" {
		p.Whitelist = strings.Split(*whitelist, ",")
	}
	if *referrers != "" {
		p.Referrers = strings.Split(*referrers, ",")
	}
	if *signatureKey != "" {
		key := []byte(*signatureKey)
		if strings.HasPrefix(*signatureKey, "@") {
			file := strings.TrimPrefix(*signatureKey, "@")
			var err error
			key, err = ioutil.ReadFile(file)
			if err != nil {
				log.Fatalf("error reading signature file: %v", err)
			}
		}
		p.SignatureKey = key
	}
	if *baseURL != "" {
		var err error
		p.DefaultBaseURL, err = url.Parse(*baseURL)
		if err != nil {
			log.Fatalf("error parsing baseURL: %v", err)
		}
	}

	p.ScaleUp = *scaleUp

	go freeMemory()

	server := &http.Server{
		Addr:    *addr,
		Handler: p,
	}

	fmt.Printf("imageproxy (version %v [build: %s, git hash: %s]) listening on %s\n", Version, BuildDate, GitHash, server.Addr)
	log.Fatal(server.ListenAndServe())

}

// parseCache parses the cache-related flags and returns the specified Cache implementation.
func parseCache() (imageproxy.Cache, error) {
	if *cache == "" {
		if *cacheDir != "" {
			return diskCache(*cacheDir), nil
		}
		return nil, nil
	}

	if *cache == "memory" {
		return httpcache.NewMemoryCache(), nil
	}

	u, err := url.Parse(*cache)
	if err != nil {
		return nil, fmt.Errorf("error parsing cache flag: %v", err)
	}

	switch u.Scheme {
	case "s3":
		u.Scheme = "https"
		return s3cache.New(u.String()), nil
	case "file":
		fallthrough
	default:
		return diskCache(u.Path), nil
	}
}

func diskCache(path string) *diskcache.Cache {
	d := diskv.New(diskv.Options{
		BasePath: path,

		// For file "c0ffee", store file as "c0/ff/c0ffee"
		Transform: func(s string) []string { return []string{s[0:2], s[2:4]} },
		CacheSizeMax: 200*1024*1024,
	})
	return diskcache.NewWithDiskv(d)
}

func parseStatsd() (statsd.Statser, error) {
	var err error

	var statserClient statsd.Statser

	if len(*statsdAddr) > 0  {
		statserClient, err = statsd.New(statsd.Address(*statsdAddr), statsd.Prefix(*statsdPrefix), statsd.MaxPacketSize(512))
		if err != nil {
			log.Printf("Error creating statsd client - setting empty client")
			statserClient = &statsd.NoopClient{}
			return statserClient, nil
		}
		return statserClient, nil

	}

	statserClient = &statsd.NoopClient{}
	return statserClient, nil
}

func freeMemory() {
	for {
		debug.FreeOSMemory()
		time.Sleep(60 * time.Second)
	}

}

func parseDebug() (*os.File, error) {
	var pathName string

	pathName = "/tmp"

	pathName = filepath.Join(pathName, "imageproxy-debug.log")
	return os.OpenFile(pathName, os.O_APPEND | os.O_WRONLY | os.O_CREATE, 0666)

}