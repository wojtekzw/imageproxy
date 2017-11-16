// Package diskcache provides an implementation of httpcache.Cache that uses the diskv package
// to supplement an in-memory map with persistent storage
//
package diskcache

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"

	"github.com/bluele/gcache"
	"github.com/golang/glog"
	"github.com/peterbourgon/diskv"
)

const maxEventChanLen = 1024

// Cache is an implementation of httpcache.Cache that supplements the in-memory map with persistent storage
type Cache struct {
	d *diskv.Diskv

	// key cache for maintaining usage of keys, when not used will help do Erase real data from d1
	kc gcache.Cache

	eventC chan eventMsg
	//  lost messages in sending to channel eg. no receivers
	lost int

	m sync.Mutex
}

// Get returns the response corresponding to key if present
func (c *Cache) Get(key string) (resp []byte, ok bool) {
	f := keyToFilename(key)
	resp, err := c.d.Read(f)
	c.send(getOp, key, f, err)
	if err != nil {
		return []byte{}, false
	}
	// increment key usage
	c.kc.Get(f)
	return resp, true
}

// Set saves a response to the cache as key
func (c *Cache) Set(key string, resp []byte) {
	f := keyToFilename(key)
	err := c.d.WriteStream(f, bytes.NewReader(resp), true)
	c.send(setOp, key, f, err)
	// increment key usage
	c.kc.Set(f, struct{}{})
}

// Delete removes the response with key from the cache
func (c *Cache) Delete(key string) {
	f := keyToFilename(key)
	err := c.d.Erase(f)
	c.send(deleteOp, key, f, err)
	// remove from key cache
	c.kc.Remove(f)
}

func (c *Cache) events() <-chan eventMsg {
	return c.eventC
}

func (c *Cache) send(op op, key, file string, err error) {
	c.m.Lock()
	defer c.m.Unlock()

	if len(c.eventC) == maxEventChanLen {
		c.lost++
		return
	}
	c.eventC <- eventMsg{e: op, key: key, file: file, err: err}
}

type op uint8

const (
	getOp = op(iota)
	setOp
	deleteOp
)

type eventMsg struct {
	e    op
	key  string
	file string
	err  error
}

func (em *eventMsg) StringOp() string {
	s := ""
	switch em.e {
	case getOp:
		s = "get"
	case setOp:
		s = "set"
	case deleteOp:
		s = "delete"
	default:
		s = "unknown"
	}
	return s
}

func cleanupOps(c *Cache) {
	ec := c.events()
	cleanCache := gcache.New(512).LFU().EvictedFunc(func(key, value interface{}) {
		c.Delete(key.(string))
	}).Build()

	for {

		select {
		case ev := <-ec:
			// fmt.Printf("Op: %s, key: %s, file: %s, err: %t\n", ev.StringOp(), ev.key, ev.file, ev.err != nil)
			if ev.e == setOp && ev.err == nil && toDel(ev.key) {
				glog.Infof("to delete: %s", ev.key)
				cleanCache.Set(ev.key, ev)
			}
			if ev.e == deleteOp {
				glog.Infof("cache delete: %s, err: %v", ev.key, ev.err)
			}
		}
	}
}

func toDel(key string) bool {
	i := strings.Index(key, "#")
	return i == -1
}

func keyToFilename(key string) string {
	h := md5.New()
	io.WriteString(h, key)
	s := hex.EncodeToString(h.Sum(nil))
	return s
}

func loadKeysFromDisk(basePath string, kc gcache.Cache) {
	err := filepath.Walk(basePath, func(path string, f os.FileInfo, err error) error {
		if err == nil && !f.IsDir() {
			kc.Set(filepath.Base(path), struct{}{})
		}
		return err
	})

	if err != nil {
		glog.Errorf("Error loading keys from disk: %v,", err)
	}
	glog.Infof("loaded keys from disk: %d", kc.Len())
}

// New returns a new Cache that will store files in basePath
func New(basePath string) *Cache {

	d := diskv.New(diskv.Options{
		BasePath:     path.Join(basePath, ""),
		CacheSizeMax: 100 * 1024 * 1024, // 100MB
	})

	kc := gcache.New(1000000).LFU().EvictedFunc(func(key, value interface{}) {
		d.Erase(key.(string))
	}).Build()

	loadKeysFromDisk(path.Join(basePath, ""), kc)

	c := &Cache{
		d:      d,
		kc:     kc,
		eventC: make(chan eventMsg, maxEventChanLen),
	}

	go cleanupOps(c)

	return c
}

// NewWithDiskv returns a new Cache using the provided Diskv as underlying
// storage.
func NewWithDiskv(d *diskv.Diskv) *Cache {
	kc := gcache.New(1000000).LFU().EvictedFunc(func(key, value interface{}) {
		d.Erase(key.(string))
	}).Build()

	loadKeysFromDisk(path.Join(d.BasePath, ""), kc)

	c := &Cache{
		d:      d,
		kc:     kc,
		eventC: make(chan eventMsg, maxEventChanLen),
	}
	go cleanupOps(c)
	return c
}
