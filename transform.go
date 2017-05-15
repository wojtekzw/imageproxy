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
	"image"
	_ "image/gif" // register gif format
	"image/jpeg"
	"image/png"

	"github.com/disintegration/imaging"
	"willnorris.com/go/gifresize"
	"math"
)

// default compression quality of resized jpegs
const defaultQuality = 95

// resample filter used when resizing images
var resampleFilter = imaging.Lanczos

// Transform the provided image.  img should contain the raw bytes of an
// encoded image in one of the supported formats (gif, jpeg, or png).  The
// bytes of a similarly encoded image is returned.
func Transform(img []byte, opt Options) ([]byte, error) {
	if !opt.transform() {
		// bail if no transformation was requested
		return img, nil
	}

	// decode image
	m, format, err := image.Decode(bytes.NewReader(img))
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
			w = imgW
		}
		if h > imgH {
			h = imgH
		}
	}

	// if requested width and height match the original, skip resizing
	if (w == imgW || w == 0) && (h == imgH || h == 0) {
		return 0, 0, false
	}

	return w, h, true
}

// cropParams calculates crop rectangle parameters to keep it in image bounds
func cropParams(m image.Image, opt Options) (x0, y0, x1, y1 int, crop bool) {
	// crop params not set
	if opt.CropHeight <= 0 || opt.CropWidth <= 0 {
		return 0, 0, 0, 0, false
	}

	imgW := m.Bounds().Max.X - m.Bounds().Min.X
	imgH := m.Bounds().Max.Y - m.Bounds().Min.Y

	x0 = opt.CropX
	y0 = opt.CropY

	// crop rectangle out of image bounds horizontally
	// -> moved to point (image_width - rectangle_width) or 0, whichever is larger
	if opt.CropX > imgW || opt.CropX + opt.CropWidth > imgW {
		x0 = int(math.Max(0, float64(imgW - opt.CropWidth)))
	}
	// crop rectangle out of image bounds vertically
	// -> moved to point (image_height - rectangle_height) or 0, whichever is larger
	if opt.CropY > imgH || opt.CropY + opt.CropHeight > imgH {
		y0 = int(math.Max(0, float64(imgH - opt.CropHeight)))
	}

	// make rectangle fit the image
	x1 = int(math.Min(float64(imgW), float64(opt.CropX + opt.CropWidth)))
	y1 = int(math.Min(float64(imgH), float64(opt.CropY + opt.CropHeight)))

	return x0, y0, x1, y1, true
}

// transformImage modifies the image m based on the transformations specified
// in opt.
func transformImage(m image.Image, opt Options) image.Image {
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
