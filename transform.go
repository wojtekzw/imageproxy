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
	"image"
	_ "image/gif" // register gif format
	"image/jpeg"
	"image/png"
	"io"
	"math"

	"github.com/disintegration/imaging"
	"github.com/golang/glog"
	"github.com/rwcarlsen/goexif/exif"
	"golang.org/x/image/tiff"   // register tiff format
	_ "golang.org/x/image/webp" // register webp format
	"willnorris.com/go/gifresize"

	"github.com/wojtekzw/statsd"
)


// default compression quality of resized jpegs
const defaultQuality = 95

// maximum distance into image to look for EXIF tags
const maxExifSize = 1 << 20

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

	ops := opt.transformOpts()
	sendToStatsd(Statsd, ops)

	if !opt.transform() {
		// bail if no transformation was requested
		return img, nil
	}

	Statsd.Increment("transform.request")

	var timerTransform statsd.Timinger
	timerTransform = Statsd.NewTiming()
	defer timerTransform.Send("transform.time.total")

	// decode image
	var timerDecode statsd.Timinger
	timerDecode = Statsd.NewTiming()

	m, format, err := image.Decode(bytes.NewReader(img))
	timerDecode.Send("transform.time.decode")

	if err != nil {
		return nil, err
	}

	// apply EXIF orientation for jpeg and tiff source images. Read at most
	// up to maxExifSize looking for EXIF tags.
	if format == "jpeg" || format == "tiff" {
		r := io.LimitReader(bytes.NewReader(img), maxExifSize)
		if exifOpt := exifOrientation(r); exifOpt.transform() {
			m = transformImage(m, exifOpt)
		}
	}

	// encode webp and tiff as jpeg by default
	if format == "tiff" || format == "webp" {
		format = "jpeg"
	}

	if opt.Format != "" {
		format = opt.Format
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
	case "tiff":
		m = transformImage(m, opt)
		err = tiff.Encode(buf, m, &tiff.Options{Compression: tiff.Deflate, Predictor: true})
		if err != nil {
			return nil, err
		}
	default:
		return nil, fmt.Errorf("unsupported format: %v", format)
	}

	imgSize.transformed = len(buf.Bytes())

	glog.Infof("transform: name: %s, initial size: %d, transformed: %d, sum: %d", url, imgSize.initial, imgSize.transformed, imgSize.initial+imgSize.transformed)

	return buf.Bytes(), nil
}

// evaluateFloat interprets the option value f. If f is between 0 and 1, it is
// interpreted as a percentage of max, otherwise it is treated as an absolute
// value.  If f is less than 0, 0 is returned.
func evaluateFloat(f float64, max int) int {
	if 0 < f && f < 1 {
		return int(float64(max) * f)
	}
	if f < 0 {
		return 0
	}
	return int(f)
}

// resizeParams determines if the image needs to be resized, and if so, the
// dimensions to resize to.
func resizeParams(m image.Image, opt Options) (w, h int, resize bool) {
	// convert percentage width and height values to absolute values
	imgW := m.Bounds().Max.X - m.Bounds().Min.X
	imgH := m.Bounds().Max.Y - m.Bounds().Min.Y
	w = evaluateFloat(opt.Width, imgW)
	h = evaluateFloat(opt.Height, imgH)

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

	glog.Infof("resizeParams: requested size: (%dx%d), calculated:(%dx%d), original: (%dx%d)", w, h, nW, nH, imgW, imgH)

	return w, h, true
}

// cropParams calculates crop rectangle parameters to keep it in image bounds
func cropParams(m image.Image, opt Options) (x0, y0, x1, y1 int, crop bool) {
	if opt.CropX == 0 && opt.CropY == 0 && opt.CropWidth == 0 && opt.CropHeight == 0 {
		return 0, 0, 0, 0, false
	}

	// width and height of image
	imgW := m.Bounds().Max.X - m.Bounds().Min.X
	imgH := m.Bounds().Max.Y - m.Bounds().Min.Y

	// top left coordinate of crop
	x0 = evaluateFloat(math.Abs(opt.CropX), imgW)
	if opt.CropX < 0 {
		x0 = imgW - x0
	}
	y0 = evaluateFloat(math.Abs(opt.CropY), imgH)
	if opt.CropY < 0 {
		y0 = imgH - y0
	}

	// width and height of crop
	w := evaluateFloat(opt.CropWidth, imgW)
	if w == 0 {
		w = imgW
	}
	h := evaluateFloat(opt.CropHeight, imgH)
	if h == 0 {
		h = imgH
	}

	if x0 == 0 && y0 == 0 && w == imgW && h == imgH {
		return 0, 0, 0, 0, false
	}

	// bottom right coordinate of crop
	x1 = x0 + w
	if x1 > imgW {
		x1 = imgW
	}
	y1 = y0 + h
	if y1 > imgH {
		y1 = imgH
	}

	return x0, y0, x1, y1, true
}

// read EXIF orientation tag from r and adjust opt to orient image correctly.
func exifOrientation(r io.Reader) (opt Options) {
	// Exif Orientation Tag values
	// http://sylvana.net/jpegcrop/exif_orientation.html
	const (
		topLeftSide     = 1
		topRightSide    = 2
		bottomRightSide = 3
		bottomLeftSide  = 4
		leftSideTop     = 5
		rightSideTop    = 6
		rightSideBottom = 7
		leftSideBottom  = 8
	)

	ex, err := exif.Decode(r)
	if err != nil {
		return opt
	}
	tag, err := ex.Get(exif.Orientation)
	if err != nil {
		return opt
	}
	orient, err := tag.Int(0)
	if err != nil {
		return opt
	}

	switch orient {
	case topLeftSide:
		// do nothing
	case topRightSide:
		opt.FlipHorizontal = true
	case bottomRightSide:
		opt.Rotate = 180
	case bottomLeftSide:
		opt.FlipVertical = true
	case leftSideTop:
		opt.Rotate = 90
		opt.FlipVertical = true
	case rightSideTop:
		opt.Rotate = -90
	case rightSideBottom:
		opt.Rotate = 90
		opt.FlipHorizontal = true
	case leftSideBottom:
		opt.Rotate = 90
	}
	return opt
}

// transformImage modifies the image m based on the transformations specified
// in opt.
func transformImage(m image.Image, opt Options) image.Image {

	var timerTransform statsd.Timinger
	timerTransform = Statsd.NewTiming()
	defer timerTransform.Send("transform.time.transform_image")

	// crop if needed
	if x0, y0, x1, y1, crop := cropParams(m, opt); crop {
		m = imaging.Crop(m, image.Rect(x0, y0, x1, y1))
	}
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

	// rotate
	rotate := float64(opt.Rotate) - math.Floor(float64(opt.Rotate)/360)*360
	switch rotate {
	case 90:
		m = imaging.Rotate90(m)
	case 180:
		m = imaging.Rotate180(m)
	case 270:
		m = imaging.Rotate270(m)
	}

	// flip
	if opt.FlipVertical {
		m = imaging.FlipV(m)
	}
	if opt.FlipHorizontal {
		m = imaging.FlipH(m)
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

func sendToStatsd(s statsd.Statser, ops transOpts) {
	if ops.quality {
		s.Increment("transform.quality")
	}
	if ops.signature {
		s.Increment("transform.signature")
	}

	if ops.absCrop {
		s.Increment("transform.abs_crop")
	}

	if ops.flipH {
		s.Increment("transform.flip_h")
	}

	if ops.flipV {
		s.Increment("transform.flip_v")
	}
	if ops.rotate {
		s.Increment("transform.rotate")
	}

	if ops.resize {
		s.Increment("transform.resize")
	}

	if ops.fit {
		s.Increment("transform.fit")
	}

	if !ops.transform {
		s.Increment("transform.noop")
	} else {
		s.Increment("transform.transform")
	}
}
