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
	"github.com/disintegration/imaging"
	"github.com/golang/glog"
	"image"
	_ "image/gif" // register gif format
	"image/jpeg"
	"image/png"
	"willnorris.com/go/gifresize"
	"github.com/wojtekzw/statsd"
)

// default compression quality of resized jpegs
const defaultQuality = 95

// resample filter used when resizing images
var resampleFilter = imaging.Lanczos

// MaxScaleUp - ff ScaleUp is allowed - maximum increase in pixel count of the image (resize from 100x100 to 200x200 is 4 times increase not 2)
var MaxScaleUp = 2.0

type imageSizes struct {
	initial            int
	initialDecoded     int
	transformedDecoded int
	transformed        int
}

// Transform the provided image.  img should contain the raw bytes of an
// encoded image in one of the supported formats (gif, jpeg, or png).  The
// bytes of a similarly encoded image is returned.
func Transform(img []byte, opt Options, url string) ([]byte, error) {

	imgSize := imageSizes{initial: len(img)}


	if !opt.transform() {
		// bail if no transformation was requested
		Statsd.Increment("transform.noop")
		return img, nil
	}

	Statsd.Increment("transform.request")

	var timerTransform statsd.Timinger
	timerTransform = Statsd.NewTiming()
	defer timerTransform.Send("transform.time.total")


	glog.Infof("pre-transform: name: %s, initial size: %d", url, imgSize.initial)

	// decode image
	var timerDecode statsd.Timinger
	timerDecode = Statsd.NewTiming()

	m, format, err := image.Decode(bytes.NewReader(img))

	timerDecode.Send("transform.time.decode")

	glog.Infof("transform:decode name: %s, format %v, err: %v", url, format,err)
	if err != nil {
		return nil, err
	}

	// transform and encode image
	buf := new(bytes.Buffer)
	switch format {
	case "gif":
		fn := func(img image.Image) image.Image {
			return transformImage(img, opt)
		}
		err = gifresize.Process(buf, bytes.NewReader(img), fn)
		if err != nil {
			return nil, err
		}
	case "jpeg":
		quality := opt.Quality
		if quality == 0 {
			quality = defaultQuality
		}

		m = transformImage(m, opt)
		err = jpeg.Encode(buf, m, &jpeg.Options{Quality: quality})
		if err != nil {
			return nil, err
		}
	case "png":
		m = transformImage(m, opt)
		err = png.Encode(buf, m)
		if err != nil {
			return nil, err
		}
	}

	imgSize.transformed = len(buf.Bytes())

	glog.Infof("transform: name: %s, initial size: %d, transformed: %d, sum: %d", url, imgSize.initial, imgSize.transformed, imgSize.initial+imgSize.transformed)

	return buf.Bytes(), nil
}

// resizeParams determines if the image needs to be resized, and if so, the
// dimensions to resize to.
func resizeParams(m image.Image, opt Options) (w, h int, resize bool) {
	// convert percentage width and height values to absolute values
	imgW := m.Bounds().Max.X - m.Bounds().Min.X
	imgH := m.Bounds().Max.Y - m.Bounds().Min.Y
	if 0 < opt.Width && opt.Width < 1 {
		w = int(float64(imgW) * opt.Width)
	} else if opt.Width < 0 {
		w = 0
	} else {
		w = int(opt.Width)
	}
	if 0 < opt.Height && opt.Height < 1 {
		h = int(float64(imgH) * opt.Height)
	} else if opt.Height < 0 {
		h = 0
	} else {
		h = int(opt.Height)
	}

	// never resize larger than the original image unless specifically allowed
	if !opt.ScaleUp {
		if w > imgW {
			glog.Infof("resizeParams: requested size: (width: %d). ScaleUp not allowed. Returning original size: (width: %d)", w, imgW)
			w = imgW
		}
		if h > imgH {
			glog.Infof("resizeParams: requested size: (height: %d). ScaleUp not allowed. Returning original size: (height: %d)", h, imgH)
			h = imgH
		}
	}

	nW, nH, err := newSize(w, h, imgW, imgH)
	if err != nil {
		return 0, 0, false
	}

	glog.Infof("resizeParams: requested size: (%dx%d), calculated:(%dx%d), original: (%dx%d)", w, h, nW, nH, imgW, imgH)

	// check ScaleUp limits (to protect memory) - max resize is set to 2 times more pixels
	if opt.ScaleUp {
		orgSize := float64(imgW * imgH)
		newSize := float64(nW * nH)
		if newSize/orgSize > MaxScaleUp {
			glog.Infof("resizeParams: requested size: (%dx%d). ScaleUp too large: %.1f (allowed: %.1f). Returning original size: (%dx%d)", w, h, newSize/orgSize, MaxScaleUp, imgW, imgH)
			// return original size
			w = imgW
			h = imgH
		}
	}

	// if requested width and height match the original, skip resizing
	if (w == imgW || w == 0) && (h == imgH || h == 0) {
		return 0, 0, false
	}

	return w, h, true
}

// transformImage modifies the image m based on the transformations specified
// in opt.
func transformImage(m image.Image, opt Options) image.Image {

	var timerTransform statsd.Timinger
	timerTransform = Statsd.NewTiming()
	defer timerTransform.Send("transform.time.transform_image")

	// resize if needed
	if w, h, resize := resizeParams(m, opt); resize {
		if opt.Fit {
			m = imaging.Fit(m, w, h, resampleFilter)
		} else {
			if w == 0 || h == 0 {
				m = imaging.Resize(m, w, h, resampleFilter)
			} else {
				m = imaging.Thumbnail(m, w, h, resampleFilter)
			}
		}
	}

	// flip
	if opt.FlipVertical {
		m = imaging.FlipV(m)
	}
	if opt.FlipHorizontal {
		m = imaging.FlipH(m)
	}

	// rotate
	switch opt.Rotate {
	case 90:
		m = imaging.Rotate90(m)
	case 180:
		m = imaging.Rotate180(m)
	case 270:
		m = imaging.Rotate270(m)
	}

	return m
}

func newSize(newW, newH, orgW, orgH int) (int, int, error) {

	if (newW > 0) && (newH > 0) {
		return newW, newH, nil
	}

	if (orgW == 0) || (orgH == 0) {
		return orgW, orgH, nil
	}

	if (newW == 0) && (newH == 0) {
		return 0, 0, fmt.Errorf("Width or Height (or both) must be greater than 0")
	}

	aspectRatio := float64(orgW) / float64(orgH)

	if newW == 0 {
		return int(aspectRatio * float64(newH)), newH, nil
	}

	if newH == 0 {
		return newW, int((1.0 / aspectRatio) * float64(newW)), nil
	}

	return newW, newH, nil
}
