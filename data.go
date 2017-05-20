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

package imageproxy

import (
	"bytes"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
)

const (
	optFit             = "fit"
	optFlipVertical    = "fv"
	optFlipHorizontal  = "fh"
	optRotatePrefix    = "r"
	optQualityPrefix   = "q"
	optSignaturePrefix = "s"
	optSizeDelimiter   = "x"
	optScaleUp         = "scaleUp"
	optCropWidth       = "cw"
	optCropHeight      = "ch"
	optCropX           = "cx"
	optCropY           = "cy"
)

// URLError reports a malformed URL error.
type URLError struct {
	Message string
	URL     *url.URL
}

func (e URLError) Error() string {
	return fmt.Sprintf("malformed URL %q: %s", e.URL, e.Message)
}

// Options specifies transformations to be performed on the requested image.
type Options struct {
	// See ParseOptions for interpretation of Width and Height values
	Width  float64
	Height float64

	// If true, resize the image to fit in the specified dimensions.  Image
	// will not be cropped, and aspect ratio will be maintained.
	Fit bool

	// Rotate image the specified degrees counter-clockwise.  Valid values
	// are 90, 180, 270.
	Rotate int

	FlipVertical   bool
	FlipHorizontal bool

	// Quality of output image
	Quality int

	// HMAC Signature for signed requests.
	Signature string

	// Allow image to scale beyond its original dimensions.  This value
	// will always be overwritten by the value of Proxy.ScaleUp.
	ScaleUp bool

	// Crop rectangle params
	CropWidth int
	CropHeight int
	CropX int
	CropY int
}

func (o Options) String() string {
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%v%s%v", o.Width, optSizeDelimiter, o.Height)
	if o.Fit {
		fmt.Fprintf(buf, ",%s", optFit)
	}
	if o.Rotate != 0 {
		fmt.Fprintf(buf, ",%s%d", string(optRotatePrefix), o.Rotate)
	}
	if o.FlipVertical {
		fmt.Fprintf(buf, ",%s", optFlipVertical)
	}
	if o.FlipHorizontal {
		fmt.Fprintf(buf, ",%s", optFlipHorizontal)
	}
	if o.Quality != 0 {
		fmt.Fprintf(buf, ",%s%d", string(optQualityPrefix), o.Quality)
	}
	if o.Signature != "" {
		fmt.Fprintf(buf, ",%s%s", string(optSignaturePrefix), o.Signature)
	}
	if o.ScaleUp {
		fmt.Fprintf(buf, ",%s", optScaleUp)
	}
	if o.CropWidth != 0 {
		fmt.Fprintf(buf, ",%s%d", optCropWidth, o.CropWidth)
	}
	if o.CropHeight != 0 {
		fmt.Fprintf(buf, ",%s%d", optCropHeight, o.CropHeight)
	}
	if o.CropX != 0 {
		fmt.Fprintf(buf, ",%s%d", optCropX, o.CropX)
	}
	if o.CropY != 0 {
		fmt.Fprintf(buf, ",%s%d", optCropY, o.CropY)
	}
	return buf.String()
}

// transform returns whether o includes transformation options.  Some fields
// are not transform related at all (like Signature), and others only apply in
// the presence of other fields (like Fit and Quality).
func (o Options) transform() bool {
	return o.Width != 0 || o.Height != 0 || o.Rotate != 0 || o.FlipHorizontal || o.FlipVertical ||
		(o.CropHeight != 0 && o.CropWidth != 0)
}

type transOpts struct {
	resize bool
	rotate bool
	fit bool
	quality bool
	signature bool
	flipH bool
	flipV bool
	absCrop bool
	transform bool
}


func (o Options) transformOpts() transOpts {

	ops := transOpts{}

	if o.Width != 0 || o.Height != 0 {
		ops.resize = true
	}

	if o.Rotate != 0 {
		ops.rotate = true
	}

	if o.CropHeight != 0 && o.CropWidth != 0 {
		ops.absCrop = true
	}

	if o.Signature != "" {
		ops.signature = true
	}

	if o.Quality != 0 {
		ops.quality = true
	}

	ops.flipH = o.FlipHorizontal
	ops.flipV = o.FlipVertical
	ops.fit = o.Fit

	ops.transform = o.transform()

	return ops

}


// ParseOptions parses str as a list of comma separated transformation options.
// The following options can be specified in any order:
//
// Size and Cropping
//
// The size option takes the general form "{width}x{height}", where width and
// height are numbers. Integer values greater than 1 are interpreted as exact
// pixel values. Floats between 0 and 1 are interpreted as percentages of the
// original image size. If either value is omitted or set to 0, it will be
// automatically set to preserve the aspect ratio based on the other dimension.
// If a single number is provided (with no "x" separator), it will be used for
// both height and width.
//
// Depending on the size options specified, an image may be cropped to fit the
// requested size. In all cases, the original aspect ratio of the image will be
// preserved; imageproxy will never stretch the original image.
//
// When no explicit crop mode is specified, the following rules are followed:
//
// - If both width and height values are specified, the image will be scaled to
// fill the space, cropping if necessary to fit the exact dimension.
//
// - If only one of the width or height values is specified, the image will be
// resized to fit the specified dimension, scaling the other dimension as
// needed to maintain the aspect ratio.
//
// If the "fit" option is specified together with a width and height value, the
// image will be resized to fit within a containing box of the specified size.
// As always, the original aspect ratio will be preserved. Specifying the "fit"
// option with only one of either width or height does the same thing as if
// "fit" had not been specified.
//
// Rotation and Flips
//
// The "r{degrees}" option will rotate the image the specified number of
// degrees, counter-clockwise. Valid degrees values are 90, 180, and 270.
//
// The "fv" option will flip the image vertically. The "fh" option will flip
// the image horizontally. Images are flipped after being rotated.
//
// Quality
//
// The "q{qualityPercentage}" option can be used to specify the quality of the
// output file (JPEG only)
//
// Examples
//
// 	0x0         - no resizing
// 	200x        - 200 pixels wide, proportional height
// 	0.15x       - 15% original width, proportional height
// 	x100        - 100 pixels tall, proportional width
// 	100x150     - 100 by 150 pixels, cropping as needed
// 	100         - 100 pixels square, cropping as needed
// 	150,fit     - scale to fit 150 pixels square, no cropping
// 	100,r90     - 100 pixels square, rotated 90 degrees
// 	100,fv,fh   - 100 pixels square, flipped horizontal and vertical
// 	200x,q80    - 200 pixels wide, proportional height, 80% quality
// 	cw100,ch200 - crop fragment that starts at (0,0), is 100px wide and 200px tall
// 	cw100,ch200,cx10,cy20 - crop fragment that start at (10,20) is 100px wide and 200px tall
func ParseOptions(str string) Options {
	var options Options

	for _, opt := range strings.Split(str, ",") {
		switch {
		case len(opt) == 0:
			break
		case opt == optFit:
			options.Fit = true
		case opt == optFlipVertical:
			options.FlipVertical = true
		case opt == optFlipHorizontal:
			options.FlipHorizontal = true
		case opt == optScaleUp: // this option is intentionally not documented above
			options.ScaleUp = true
		case strings.HasPrefix(opt, optRotatePrefix):
			value := strings.TrimPrefix(opt, optRotatePrefix)
			options.Rotate, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optQualityPrefix):
			value := strings.TrimPrefix(opt, optQualityPrefix)
			options.Quality, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optSignaturePrefix):
			options.Signature = strings.TrimPrefix(opt, optSignaturePrefix)
		case strings.HasPrefix(opt, optCropHeight):
			value := strings.TrimPrefix(opt, optCropHeight)
			options.CropHeight, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optCropWidth):
			value := strings.TrimPrefix(opt, optCropWidth)
			options.CropWidth, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optCropX):
			value := strings.TrimPrefix(opt, optCropX)
			options.CropX, _ = strconv.Atoi(value)
		case strings.HasPrefix(opt, optCropY):
			value := strings.TrimPrefix(opt, optCropY)
			options.CropY, _ = strconv.Atoi(value)
		case strings.Contains(opt, optSizeDelimiter):
			size := strings.SplitN(opt, optSizeDelimiter, 2)
			if w := size[0]; w != "" {
				options.Width, _ = strconv.ParseFloat(w, 64)
			}
			if h := size[1]; h != "" {
				options.Height, _ = strconv.ParseFloat(h, 64)
			}
		default:
			if size, err := strconv.ParseFloat(opt, 64); err == nil {
				options.Width = size
				options.Height = size
			}
		}
	}

	return options
}

// Request is an imageproxy request which includes a remote URL of an image to
// proxy, and an optional set of transformations to perform.
type Request struct {
	URL      *url.URL      // URL of the image to proxy
	Options  Options       // Image transformation to perform
	Original *http.Request // The original HTTP request
}

// String returns the request URL as a string, with r.Options encoded in the
// URL fragment.
func (r Request) String() string {
	u := *r.URL
	u.Fragment = r.Options.String()
	return u.String()
}

// NewRequest parses an http.Request into an imageproxy Request.  Options and
// the remote image URL are specified in the request path, formatted as:
// /{options}/{remote_url}.  Options may be omitted, so a request path may
// simply contain /{remote_url}.  The remote URL must be an absolute "http" or
// "https" URL, should not be URL encoded, and may contain a query string.
//
// Assuming an imageproxy server running on localhost, the following are all
// valid imageproxy requests:
//
// 	http://localhost/100x200/http://example.com/image.jpg
// 	http://localhost/100x200,r90/http://example.com/image.jpg?foo=bar
// 	http://localhost//http://example.com/image.jpg
// 	http://localhost/http://example.com/image.jpg
func NewRequest(r *http.Request, baseURL *url.URL) (*Request, error) {
	var err error
	req := &Request{Original: r}

	path := r.URL.Path[1:] // strip leading slash
	req.URL, err = parseURL(path)
	if err != nil || !req.URL.IsAbs() {
		// first segment should be options
		parts := strings.SplitN(path, "/", 2)
		if len(parts) != 2 {
			return nil, URLError{"too few path segments", r.URL}
		}

		var err error
		req.URL, err = parseURL(parts[1])
		if err != nil {
			return nil, URLError{fmt.Sprintf("unable to parse remote URL: %v", err), r.URL}
		}

		req.Options = ParseOptions(parts[0])
	}

	if baseURL != nil {
		req.URL = baseURL.ResolveReference(req.URL)
	}

	if !req.URL.IsAbs() {
		return nil, URLError{"must provide absolute remote URL", r.URL}
	}

	if req.URL.Scheme != "http" && req.URL.Scheme != "https" {
		return nil, URLError{"remote URL must have http or https scheme", r.URL}
	}

	// query string is always part of the remote URL
	req.URL.RawQuery = r.URL.RawQuery
	return req, nil
}

var reCleanedURL = regexp.MustCompile(`^(https?):/+([^/])`)

// parseURL parses s as a URL, handling URLs that have been munged by
// path.Clean or a webserver that collapses multiple slashes.
func parseURL(s string) (*url.URL, error) {
	s = reCleanedURL.ReplaceAllString(s, "$1://$2")
	return url.Parse(s)
}
