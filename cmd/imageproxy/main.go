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
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bluele/gcache"
	"github.com/wojtekzw/limitedcache"

	"github.com/PaulARoy/azurestoragecache"
	"github.com/diegomarangoni/gcscache"
	"github.com/garyburd/redigo/redis"
	"github.com/gregjones/httpcache"
	"github.com/gregjones/httpcache/diskcache"
	rediscache "github.com/gregjones/httpcache/redis"
	"github.com/peterbourgon/diskv"
	"github.com/wojtekzw/imageproxy"
	"github.com/wojtekzw/imageproxy/internal/s3cache"
	"github.com/wojtekzw/imageproxy/ip"
	"github.com/wojtekzw/statsd"
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
var whitelistIP = flag.String("whitelistIP", "", "comma separated list of allowed remote hosts IP ranges. Ranges is defined as in 192.168.1.100-192.168.120 or 192.168.10.0/24 or 127.0.0.1 ")
var referrers = flag.String("referrers", "", "comma separated list of allowed referring hosts")
var baseURL = flag.String("baseURL", "", "default base URL for relative remote URLs")
var cache = flag.String("cache", "", "location to cache images (see https://github.com/wojtekzw/imageproxy#cache)")
var cacheLimit = flag.Uint("cacheLimit", 2000000, "maximum number of items in disk cache")
var responseSize = flag.Uint64("responseSize", imageproxy.MaxRespBodySize, "Max size of original proxied request")
var signatureKey = flag.String("signatureKey", "", "HMAC key used in calculating request signatures")
var scaleUp = flag.Bool("scaleUp", false, "allow images to scale beyond their original dimensions")
var maxScaleUp = flag.Float64("maxScaleUp", imageproxy.MaxScaleUp, "limit scaleUp to maxScaleUp times (eg. 4.0 means 100x100 can be resized do 200x200 or 300x133 etc.)")
var timeout = flag.Duration("timeout", 30*time.Second, "time limit for requests served by this proxy")
var version = flag.Bool("version", false, "print version information")
var printConfig = flag.Bool("printConfig", false, "print config")
var statsdAddr = flag.String("statsdAddr", ":8125", "UDP address of Statsd compatible server")
var statsdPrefix = flag.String("statsdPrefix", "imageproxy", "prefix of Statsd data names")
var httpProxy = flag.String("httpProxy", "", "HTTP_PROXY URL to be used")
var sslSkipVerify = flag.Bool("sslSkipVerify", false, "skip verify of SSL ceritficate (enable self-signed or expired certificates)")

func main() {

	flag.Parse()
	// log_dir flag added by golang/glog
	var logDir = flag.Lookup("log_dir").Value.(flag.Getter).Get().(string)

	if *version {
		fmt.Printf("Version: %v\nBuild: %v\nGitHash: %v\n", Version, BuildDate, GitHash)
		os.Exit(0)
	}

	parseLog(logDir)

	c, err := parseCache()
	if err != nil {
		log.Fatal(err)
	}

	imageproxy.Statsd, err = parseStatsd()
	if err != nil {
		log.Fatal(err)
	}

	imageproxy.Statsd.Increment("exec.started")
	proxyURL, err := url.Parse(*httpProxy)
	if err == nil {
		os.Setenv("HTTP_PROXY", proxyURL.String())
	}

	if imageproxy.VipsEnabled {
		log.Printf("using VIPS C library to resize images")
	} else {
		log.Printf("using standard Go libraries to resize images")
	}
	if *responseSize == 0 {
		*responseSize = imageproxy.MaxRespBodySize
		log.Printf("set responseSize to %d", *responseSize)
	}

	if *maxScaleUp <= 0 {
		// do nothing - leave default imageproxy.MaxScaleUp. Inform user
		log.Printf("set maxScaleUp to %.1f", imageproxy.MaxScaleUp)
	} else {
		imageproxy.MaxScaleUp = *maxScaleUp
	}

	var transport http.RoundTripper
	transport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
			DualStack: true,
		}).DialContext,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		TLSClientConfig:       &tls.Config{InsecureSkipVerify: *sslSkipVerify},
	}

	p := imageproxy.NewProxy(transport, c, *responseSize)

	if *whitelist != "" {
		p.Whitelist = strings.Split(*whitelist, ",")
	}

	if *whitelistIP != "" {
		p.WhitelistIP, err = parseWhitelistIP(*whitelistIP)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s\n", err)
			os.Exit(1)
		}
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

	p.Timeout = *timeout
	p.ScaleUp = *scaleUp

	server := &http.Server{
		Addr:    *addr,
		Handler: p,
	}

	if *printConfig {
		fmt.Fprintf(os.Stderr, "version: %s\n", Version)
		fmt.Fprintf(os.Stderr, "build date: %s\n", BuildDate)
		fmt.Fprintf(os.Stderr, "git hash: %s\n", GitHash)
		fmt.Fprintf(os.Stderr, "listen addr: %s\n", *addr)
		fmt.Fprintf(os.Stderr, "http proxy (for get image): %s\n", proxyURL.String())
		fmt.Fprintf(os.Stderr, "log dir: %s\n", logDir)
		fmt.Fprintf(os.Stderr, "cache dir: %s\n", *cache)
		fmt.Fprintf(os.Stderr, "cache limit (max number of files): %d\n", *cacheLimit)
		fmt.Fprintf(os.Stderr, "vips lib enabled: %t\n", imageproxy.VipsEnabled)
		fmt.Fprintf(os.Stderr, "max response size (for get image): %d\n", *responseSize)
		fmt.Fprintf(os.Stderr, "max pixel size of image to be transformed (compiled in): %d\n", imageproxy.MaxPixels)
		fmt.Fprintf(os.Stderr, "max transform concurrency (compiled in): %d\n", imageproxy.MaxConcurrency)
		fmt.Fprintf(os.Stderr, "whitelist domains: %s\n", strings.Join(p.Whitelist, ", "))
		fmt.Fprintf(os.Stderr, "whitelist IPs: %s\n", fmt.Sprintf("%v", p.WhitelistIP))
		fmt.Fprintf(os.Stderr, "whitelist referrers: %s\n", strings.Join(p.Referrers, ", "))
		fmt.Fprintf(os.Stderr, "signature key: %s\n", p.SignatureKey)
		fmt.Fprintf(os.Stderr, "base url: %s\n", *baseURL)
		fmt.Fprintf(os.Stderr, "scale up enabled: %t\n", p.ScaleUp)
		fmt.Fprintf(os.Stderr, "max scale up: %.1f\n", imageproxy.MaxScaleUp)
		fmt.Fprintf(os.Stderr, "timeout: %s\n", p.Timeout.String())
		fmt.Fprintf(os.Stderr, "statsd addr: %s\n", *statsdAddr)
		fmt.Fprintf(os.Stderr, "statsd prefix: %s\n", *statsdPrefix)
	}

	log.Printf("imageproxy (version %v [build: %s, git hash: %s]) listening on %s", Version, BuildDate, GitHash, server.Addr)
	err = server.ListenAndServe()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		log.Fatal(err)
	}

}

// parseCache parses the cache-related flags and returns the specified Cache implementation.
func parseCache() (imageproxy.Cache, error) {
	if *cache == "" {
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
		return s3cache.New(u.String())
	case "gcs":
		return gcscache.New(u.String()), nil
	case "azure":
		return azurestoragecache.New("", "", u.Host)
	case "redis":
		conn, err := redis.DialURL(u.String(), redis.DialPassword(os.Getenv("REDIS_PASSWORD")))
		if err != nil {
			return nil, err
		}
		return rediscache.NewWithClient(conn), nil
	case "file":
		fallthrough
	default:
		return diskCache(u.Path, *cacheLimit), nil
	}
}

func diskCache(path string, limit uint) imageproxy.Cache {
	d := diskv.New(diskv.Options{
		BasePath: path,

		// For file "c0ffee", store file as "c0/ff/c0ffee"
		Transform:    func(s string) []string { return []string{s[0:2], s[2:4]} },
		CacheSizeMax: 200 * 1024 * 1024,
	})

	if limit == 0 {
		return diskcache.NewWithDiskv(d)
	}

	c := limitedcache.NewWithDiskv(d, int(limit))
	go c.LoadKeysFromDisk(d.BasePath)
	go removeFullPictFromCache(c, 512)
	return c
}

func removeFullPictFromCache(c *limitedcache.Cache, limit int) {
	ec := c.Events()
	cleanCache := gcache.New(limit).LFU().EvictedFunc(func(key, value interface{}) {
		c.Delete(key.(string))
	}).Build()

	for {

		select {
		case ev := <-ec:
			if ev.OperationID() == limitedcache.SetOp && ev.Status() == nil && toDel(ev.Key()) {
				cleanCache.Set(ev.Key(), ev)
			}

		}
	}
}

func toDel(key string) bool {
	i := strings.Index(key, "#")
	return i == -1
}

func parseStatsd() (statsd.Statser, error) {
	var err error

	var statserClient statsd.Statser

	if len(*statsdAddr) > 0 {
		statserClient, err = statsd.New(statsd.Address(*statsdAddr), statsd.Prefix(*statsdPrefix), statsd.MaxPacketSize(512))
		if err != nil {
			log.Printf("error creating statsd client - setting empty client")
			statserClient = &statsd.NoopClient{}
			return statserClient, nil
		}
		return statserClient, nil

	}

	statserClient = &statsd.NoopClient{}
	return statserClient, nil
}

func parseLog(pathName string) {

	pathName = filepath.Join(pathName, "imageproxy.log")
	f, err := os.OpenFile(pathName, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0666)
	if err != nil {
		log.Fatal(err)
	}
	log.SetOutput(f)
}

func parseWhitelistIP(whitelistIP string) ([]ip.Range, error) {
	ipRanges := []ip.Range{}
	ipRangesStrings := strings.Split(whitelistIP, ",")
	for _, v := range ipRangesStrings {
		fromIP, toIP, err := ip.ParseIPRangeString(v)
		if err != nil {
			return nil, err
		}
		ipRanges = append(ipRanges, ip.Range{From: fromIP, To: toIP})
	}

	return ipRanges, nil
}
