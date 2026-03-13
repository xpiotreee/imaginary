package main

import (
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"math"
	"net/http"
	"strings"

	"github.com/h2non/bimg"
)

// Image stores an image binary buffer and its MIME type
type Image struct {
	Body []byte
	Type string
}

// Operation defines an image transformation function
type Operation func([]byte, ImageOptions) (Image, error)

// ImageInfo represents an image metadata
type ImageInfo struct {
	Width       int    `json:"width"`
	Height      int    `json:"height"`
	Type        string `json:"type"`
	Space       string `json:"space"`
	HasAlpha    bool   `json:"hasAlpha"`
	HasProfile  bool   `json:"hasProfile"`
	Channels    int    `json:"channels"`
	Orientation int    `json:"orientation"`
}

func Info(buf []byte, o ImageOptions) (Image, error) {
	metadata, err := bimg.Metadata(buf)
	if err != nil {
		return Image{}, err
	}

	info := ImageInfo{
		Width:       metadata.Size.Width,
		Height:      metadata.Size.Height,
		Type:        metadata.Type,
		Space:       metadata.Space,
		HasAlpha:    metadata.Alpha,
		HasProfile:  metadata.Profile,
		Channels:    metadata.Channels,
		Orientation: metadata.Orientation,
	}

	body, err := json.Marshal(info)
	if err != nil {
		return Image{}, err
	}

	return Image{Body: body, Type: "application/json"}, nil
}

func Resize(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Embed = true
	if o.IsDefinedField.NoCrop {
		opts.Crop = !o.NoCrop
	}
	return Process(buf, opts)
}

func Fit(buf []byte, o ImageOptions) (Image, error) {
	metadata, err := bimg.Metadata(buf)
	if err != nil {
		return Image{}, err
	}

	dims := metadata.Size
	var originHeight, originWidth int
	var fitHeight, fitWidth *int
	if o.NoRotation || (metadata.Orientation <= 4) {
		originHeight = dims.Height
		originWidth = dims.Width
		fitHeight = &o.Height
		fitWidth = &o.Width
	} else {
		originWidth = dims.Height
		originHeight = dims.Width
		fitWidth = &o.Height
		fitHeight = &o.Width
	}
	*fitWidth, *fitHeight = calculateDestinationFitDimension(originWidth, originHeight, *fitWidth, *fitHeight)

	opts := BimgOptions(o)
	opts.Embed = true
	return Process(buf, opts)
}

func Enlarge(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Enlarge = true
	opts.Crop = !o.NoCrop
	return Process(buf, opts)
}

func Extract(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Top = o.Top
	opts.Left = o.Left
	opts.AreaWidth = o.AreaWidth
	opts.AreaHeight = o.AreaHeight
	return Process(buf, opts)
}

func Crop(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Crop = true
	return Process(buf, opts)
}

func SmartCrop(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Crop = true
	opts.Gravity = bimg.GravitySmart
	return Process(buf, opts)
}

func Rotate(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	return Process(buf, opts)
}

func AutoRotate(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	return Process(buf, opts)
}

func Flip(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Flip = true
	return Process(buf, opts)
}

func Flop(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Flop = true
	return Process(buf, opts)
}

func Thumbnail(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	return Process(buf, opts)
}

func Zoom(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Zoom = o.Factor
	return Process(buf, opts)
}

func Convert(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	return Process(buf, opts)
}

func GaussianBlur(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	return Process(buf, opts)
}

func Watermark(buf []byte, o ImageOptions) (Image, error) {
	opts := BimgOptions(o)
	opts.Watermark.Text = o.Text
	opts.Watermark.Font = o.Font
	opts.Watermark.Opacity = o.Opacity
	opts.Watermark.Width = o.TextWidth
	opts.Watermark.DPI = o.DPI
	opts.Watermark.Margin = o.Margin
	opts.Watermark.NoReplicate = o.NoReplicate

	if len(o.Color) != 0 {
		opts.Watermark.Background = bimg.Color{R: o.Color[0], G: o.Color[1], B: o.Color[2]}
	}

	return Process(buf, opts)
}

func watermarkPosition(gravity bimg.Gravity, gravityExplicit bool, img, wm bimg.ImageMetadata) (int, int) {
	if !gravityExplicit {
		return img.Size.Height - wm.Size.Height, img.Size.Width - wm.Size.Width
	}

	switch gravity {
	case bimg.GravityNorth:
		return 0, (img.Size.Width - wm.Size.Width) / 2
	case bimg.GravitySouth:
		return img.Size.Height - wm.Size.Height, (img.Size.Width - wm.Size.Width) / 2
	case bimg.GravityEast:
		return (img.Size.Height - wm.Size.Height) / 2, img.Size.Width - wm.Size.Width
	case bimg.GravityWest:
		return (img.Size.Height - wm.Size.Height) / 2, 0
	case bimg.GravityCentre:
		return (img.Size.Height - wm.Size.Height) / 2, (img.Size.Width - wm.Size.Width) / 2
	default:
		return img.Size.Height - wm.Size.Height, img.Size.Width - wm.Size.Width
	}
}

func WatermarkImage(buf []byte, o ImageOptions) (Image, error) {
	if o.Image == "" {
		return Image{}, NewError("Missing required param: image", http.StatusBadRequest)
	}

	response, err := http.Get(o.Image)
	if err != nil {
		return Image{}, NewError(fmt.Sprintf("Unable to retrieve watermark image. %s", o.Image), http.StatusBadRequest)
	}
	defer response.Body.Close()

	imageBuf, err := ioutil.ReadAll(io.LimitReader(response.Body, 1e7))
	if err != nil || len(imageBuf) == 0 {
		return Image{}, NewError("Unable to read watermark image", http.StatusBadRequest)
	}

	if o.Width > 0 {
		srcMetadata, err := bimg.Metadata(buf)
		if err != nil {
			return Image{}, NewError("unable to read source image metadata", http.StatusBadRequest)
		}
		wmMetadataPre, err := bimg.Metadata(imageBuf)
		if err != nil {
			return Image{}, NewError("unable to read watermark image metadata", http.StatusBadRequest)
		}

		targetWidth := o.Width
		if o.Width <= 100 {
			targetWidth = int(float64(srcMetadata.Size.Width) * (float64(o.Width) / 100.0))
		}
		if targetWidth > 0 && targetWidth != wmMetadataPre.Size.Width {
			ratio := float64(targetWidth) / float64(wmMetadataPre.Size.Width)
			targetHeight := int(float64(wmMetadataPre.Size.Height) * ratio)
			if targetHeight < 1 {
				targetHeight = 1
			}
			resizedWm, err := bimg.Resize(imageBuf, bimg.Options{
				Width:   targetWidth,
				Height:  targetHeight,
				Force:   true,
				Enlarge: true,
				Type:    bimg.PNG,
			})
			if err != nil {
				return Image{}, NewError(fmt.Sprintf("unable to resize watermark image: %s", err.Error()), http.StatusBadRequest)
			}
			imageBuf = resizedWm
		}
	}

	opts := BimgOptions(o)
	opts.Width = 0
	opts.Height = 0
	opts.WatermarkImage.Buf = imageBuf
	opts.WatermarkImage.Opacity = o.Opacity

	if o.Top == 0 && o.Left == 0 {
		metadata, err := bimg.Metadata(buf)
		wmMetadata, err2 := bimg.Metadata(imageBuf)
		if err == nil && err2 == nil {
			opts.WatermarkImage.Top, opts.WatermarkImage.Left = watermarkPosition(
				o.Gravity, o.IsDefinedField.Gravity, metadata, wmMetadata,
			)
		}
	} else {
		opts.WatermarkImage.Left = o.Left
		opts.WatermarkImage.Top = o.Top
	}

	return Process(buf, opts)
}

func Pipeline(buf []byte, o ImageOptions) (Image, error) {
	if len(o.Operations) == 0 {
		return Image{}, fmt.Errorf("missing pipeline operations")
	}

	result := buf
	var lastMime string

	for _, op := range o.Operations {
		// Map operation name to function
		operationFunc := getOperationFunc(op.Name)
		if operationFunc == nil {
			if op.IgnoreFailure {
				continue
			}
			return Image{}, fmt.Errorf("unsupported operation: %s", op.Name)
		}

		// Parse params into ImageOptions
		imgOpts, err := buildParamsFromOperation(op)
		if err != nil {
			return Image{}, err
		}

		img, err := operationFunc(result, imgOpts)
		if err != nil {
			if op.IgnoreFailure {
				continue
			}
			return Image{}, err
		}
		result = img.Body
		lastMime = img.Type
	}

	return Image{Body: result, Type: lastMime}, nil
}

func getOperationFunc(name string) Operation {
	switch strings.ToLower(name) {
	case "resize":
		return Resize
	case "fit":
		return Fit
	case "enlarge":
		return Enlarge
	case "extract":
		return Extract
	case "crop":
		return Crop
	case "smartcrop":
		return SmartCrop
	case "rotate":
		return Rotate
	case "autorotate":
		return AutoRotate
	case "flip":
		return Flip
	case "flop":
		return Flop
	case "thumbnail":
		return Thumbnail
	case "zoom":
		return Zoom
	case "convert":
		return Convert
	case "watermark":
		return Watermark
	case "watermarkimage":
		return WatermarkImage
	case "blur":
		return GaussianBlur
	}
	return nil
}

func Process(buf []byte, opts bimg.Options) (Image, error) {
	newBuf, err := bimg.Resize(buf, opts)
	if err != nil {
		return Image{}, err
	}

	mime := bimg.DetermineImageTypeName(newBuf)
	return Image{Body: newBuf, Type: "image/" + mime}, nil
}

func calculateDestinationFitDimension(imageWidth, imageHeight, fitWidth, fitHeight int) (int, int) {
	if imageWidth*fitHeight > fitWidth*imageHeight {
		fitHeight = int(math.Round(float64(fitWidth) * float64(imageHeight) / float64(imageWidth)))
	} else {
		fitWidth = int(math.Round(float64(fitHeight) * float64(imageWidth) / float64(imageHeight)))
	}
	return fitWidth, fitHeight
}
