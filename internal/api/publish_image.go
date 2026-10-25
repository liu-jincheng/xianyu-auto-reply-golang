package api

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
)

func publishImageDimensions(content []byte) (int, int, error) {
	if len(content) >= 12 && string(content[:4]) == "RIFF" && string(content[8:12]) == "WEBP" {
		return publishWebPDimensions(content)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(content))
	return cfg.Width, cfg.Height, err
}

// Publishing forwards the original WebP bytes; only the canvas dimensions are
// needed for the upload metadata. Read the VP8/VP8L/VP8X headers without decoding
// pixels, following https://developers.google.com/speed/webp/docs/riff_container.
func publishWebPDimensions(content []byte) (int, int, error) {
	invalid := errors.New("WebP 图片头无效")
	end := uint64(binary.LittleEndian.Uint32(content[4:8])) + 8
	if end > uint64(len(content)) || end < 20 {
		return 0, 0, invalid
	}
	chunks := content[12:int(end)]
	for len(chunks) >= 8 {
		size := uint64(binary.LittleEndian.Uint32(chunks[4:8]))
		padded := size + size%2
		if padded > uint64(len(chunks)-8) {
			return 0, 0, invalid
		}
		data := chunks[8 : 8+int(size)]
		switch string(chunks[:4]) {
		case "VP8X":
			if len(data) != 10 {
				return 0, 0, invalid
			}
			w := uint32(data[4]) | uint32(data[5])<<8 | uint32(data[6])<<16
			h := uint32(data[7]) | uint32(data[8])<<8 | uint32(data[9])<<16
			return int(w) + 1, int(h) + 1, nil
		case "VP8L":
			if len(data) < 5 || data[0] != 0x2f || data[4]>>5 != 0 {
				return 0, 0, invalid
			}
			bits := binary.LittleEndian.Uint32(data[1:5])
			return int(bits&0x3fff) + 1, int((bits>>14)&0x3fff) + 1, nil
		case "VP8 ":
			if len(data) < 10 || data[0]&1 != 0 || string(data[3:6]) != "\x9d\x01\x2a" {
				return 0, 0, invalid
			}
			w := int(binary.LittleEndian.Uint16(data[6:8]) & 0x3fff)
			h := int(binary.LittleEndian.Uint16(data[8:10]) & 0x3fff)
			if w == 0 || h == 0 {
				return 0, 0, invalid
			}
			return w, h, nil
		}
		chunks = chunks[8+int(padded):]
	}
	return 0, 0, invalid
}
