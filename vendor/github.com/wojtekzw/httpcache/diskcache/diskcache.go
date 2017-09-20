// Package diskcache provides an implementation of httpcache.Cache that uses the diskv package
// to supplement an in-memory map with persistent storage
//
package diskcache

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"github.com/wojtekzw/diskv"
	"io"
	"github.com/golang/glog"
	"github.com/bluele/gcache"
	"path/filepath"
	"os"
)

// Cache is an implementation of httpcache.Cache that supplements the in-memory map with persistent storage
type Cache struct {
	d *diskv.Diskv
	// key cache for maintaining usage of keys, when not used will help do Erase real data from d
	kc gcache.Cache
}

// Get returns the response corresponding to key if present
func (c *Cache) Get(key string) (resp []byte, ok bool) {
	key = keyToFilename(key)
	resp, err := c.d.Read(key)
	if err != nil {
		return []byte{}, false
	}
	// increment key usage
	c.kc.Get(key)
	return resp, true
}

// Set saves a response to the cache as key
func (c *Cache) Set(key string, resp []byte) {
	key = keyToFilename(key)
	c.d.WriteStream(key, bytes.NewReader(resp), true)
	// increment key usage
	c.kc.Set(key,struct{}{})
}

// Delete removes the response with key from the cache
func (c *Cache) Delete(key string) {
	key = keyToFilename(key)
	c.d.Erase(key)
	// remove from key cache
	c.kc.Remove(key)
}

func keyToFilename(key string) string {
	h := md5.New()
	io.WriteString(h, key)
	s := hex.EncodeToString(h.Sum(nil))
	return s
}

func loadKeysFromDisk(basePath string, kc gcache.Cache) {
	err := filepath.Walk(basePath, func(path string, f os.FileInfo, err error) error {
		if err ==nil && !f.IsDir() {
			kc.Set(filepath.Base(path),struct{}{})
		}
		return err
	})

	if err != nil {
		glog.Errorf("Error loading keys from disk: %v,",err)
	}
	glog.Infof("loaded keys from disk: %d",kc.Len())
}

// New returns a new Cache that will store files in basePath
func New(basePath string) *Cache {
	d := diskv.New(diskv.Options{
		BasePath:     basePath,
		CacheSizeMax: 100 * 1024 * 1024, // 100MB
	})
	kc := gcache.New(20000).ARC().EvictedFunc(func(key, value interface{}) {
		d.Erase(key.(string))
	}).Build()


	loadKeysFromDisk(basePath,kc)

	return &Cache{
		d: d,
		kc: kc, }
}


// NewWithDiskv returns a new Cache using the provided Diskv as underlying
// storage.
func NewWithDiskv(d *diskv.Diskv) *Cache {
	kc := gcache.New(20000).ARC().EvictedFunc(func(key, value interface{}) {
		d.Erase(key.(string))
	}).Build()


	loadKeysFromDisk(d.BasePath,kc)

	return &Cache{
		d: d,
		kc: kc, }
}


