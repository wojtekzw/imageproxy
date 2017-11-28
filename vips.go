// +buid darwin

package imageproxy

import (
	"fmt"
	"math"

	"github.com/golang/glog"
	"github.com/h2non/bimg"
	"github.com/wojtekzw/statsd"
)

// Transform_VIPS the provided image.  img should contain the raw bytes of an
// encoded image in one of the supported formats (gif, jpeg, or png).  The
// bytes of a similarly encoded image is returned.
func Transform_VIPS(img []byte, opt Options, url string) ([]byte, error) {

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

	glog.Infof("pre-transform: name: %s, initial size: %d", url, imgSize.initial)

	m := bimg.NewImage(img)
	metadata, err := m.Metadata()
	if err != nil {
		return nil, err
	}
	format := metadata.Type

	// apply EXIF orientation for jpeg and tiff source images. Read at most
	// up to maxExifSize looking for EXIF tags.
	if format == "jpeg" || format == "tiff" {
		r := metadata.Orientation
		if exifOpt := exifOrientation_VIPS(r); exifOpt.transform() {
			m = transformImage_VIPS(m, exifOpt)
		}
	}

	// encode webp and tiff as jpeg by default
	if format == "tiff" || format == "webp" {
		format = "jpeg"
	}

	if opt.Format != "" {
		format = opt.Format
	}

	m = transformImage_VIPS(m, opt)
	// transform and encode image

	var bm []byte

	switch format {
	case "gif":
		bm, err = m.Convert(bimg.GIF)
		if err != nil {
			return nil, err
		}
	case "jpeg":
		quality := opt.Quality
		if quality == 0 {
			quality = defaultQuality
		}
		bOpt := bimg.Options{Type: bimg.JPEG, Quality: quality}
		bm, err = m.Process(bOpt)
		if err != nil {
			return nil, err
		}
	case "png":
		bm, err = m.Convert(bimg.PNG)
		if err != nil {
			return nil, err
		}
	case "tiff":
		bm, err = m.Convert(bimg.TIFF)
		if err != nil {
			return nil, err
		}

	default:
		return nil, fmt.Errorf("unsupported format: %v", format)
	}

	imgSize.transformed = len(bm)

	glog.Infof("transform: name: %s, initial size: %d, transformed: %d, sum: %d", url, imgSize.initial, imgSize.transformed, imgSize.initial+imgSize.transformed)

	return bm, nil
}

// resizeParams_VIPS determines if the image needs to be resized, and if so, the
// dimensions to resize to.
func resizeParams_VIPS(m *bimg.Image, opt Options) (w, h int, resize bool) {
	// convert percentage width and height values to absolute values
	is, err := m.Size()
	if err != nil {
		return 0, 0, false
	}
	imgW := is.Width
	imgH := is.Height
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
	// =============== FIT ===============
	if opt.Fit {
		maxW, maxH := w, h
		if maxW <= 0 || maxH <= 0 {
			return 0, 0, false
		}

		srcW := imgW
		srcH := imgH

		if srcW <= 0 || srcH <= 0 {
			return 0, 0, false
		}

		if srcW <= maxW && srcH <= maxH {
			return srcW, srcH, false
		}

		srcAspectRatio := float64(srcW) / float64(srcH)
		maxAspectRatio := float64(maxW) / float64(maxH)

		var newW, newH int
		if srcAspectRatio > maxAspectRatio {
			newW = maxW
			newH = int(float64(newW) / srcAspectRatio)
		} else {
			newH = maxH
			newW = int(float64(newH) * srcAspectRatio)
		}

		w = newW
		h = newH
	}

	// =============== END FIT ===============

	// if requested width and height match the original, skip resizing
	if (w == imgW || w == 0) && (h == imgH || h == 0) {
		return 0, 0, false
	}

	glog.Infof("resizeParams: requested size: (%dx%d), calculated:(%dx%d), original: (%dx%d)", w, h, nW, nH, imgW, imgH)

	return w, h, true
}

// cropParams calculates crop rectangle parameters to keep it in image bounds
func cropParams_VIPS(m *bimg.Image, opt Options) (x0, y0, width, height int, crop bool) {
	if opt.CropX == 0 && opt.CropY == 0 && opt.CropWidth == 0 && opt.CropHeight == 0 {
		return 0, 0, 0, 0, false
	}

	is, err := m.Size()
	if err != nil {
		return 0, 0, 0, 0, false
	} // width and height of image
	imgW := is.Width
	imgH := is.Height

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

	return x0, y0, w, h, true
}

// read EXIF orientation tag from r and adjust opt to orient image correctly.
func exifOrientation_VIPS(orient int) (opt Options) {
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

// transformImage_VIPS modifies the image m based on the transformations specified
// in opt.
func transformImage_VIPS(m *bimg.Image, opt Options) *bimg.Image {

	var bm []byte

	var timerTransform statsd.Timinger
	timerTransform = Statsd.NewTiming()
	defer timerTransform.Send("transform.time.transform_image")

	// crop if needed
	if x0, y0, w, h, crop := cropParams_VIPS(m, opt); crop {
		bm, _ = m.Extract(y0, x0, w, h)
		m = bimg.NewImage(bm)
	}

	// resize if needed
	if w, h, resize := resizeParams_VIPS(m, opt); resize {
		options := bimg.Options{
			Width:        w,
			Height:       h,
			Embed:        true,
			Interpolator: bimg.Bicubic,
			Quality:      80,
		}
		bm, _ = m.Process(options)
		//  bm, _ = m.Resize(w, h)
		m = bimg.NewImage(bm)
	}

	// rotate
	rotate := float64(opt.Rotate) - math.Floor(float64(opt.Rotate)/360)*360
	switch rotate {
	case 90:
		bm, _ = m.Rotate(270)
		m = bimg.NewImage(bm)
	case 180:
		bm, _ = m.Rotate(180)
		m = bimg.NewImage(bm)
	case 270:
		bm, _ = m.Rotate(90)
		m = bimg.NewImage(bm)
	}

	// flip
	if opt.FlipVertical {
		bm, _ = m.Flip()
		m = bimg.NewImage(bm)
	}
	if opt.FlipHorizontal {
		bm, _ = m.Flop()
		m = bimg.NewImage(bm)
	}

	return m
}
