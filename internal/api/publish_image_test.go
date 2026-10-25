package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/zhinianboke/xianyu-auto-go/internal/config"
	"github.com/zhinianboke/xianyu-auto-go/internal/database"
)

// Real 3x2 images covering the three WebP dimension header formats.
var publishWebPFixtures = map[string]string{
	"lossy":    "UklGRjwAAABXRUJQVlA4IDAAAADQAQCdASoDAAIAAUAmJaACdLoB+AADsAD+8ut//NgVzXPv9//S4P0uD9Lg/9KQAAA=",
	"lossless": "UklGRhwAAABXRUJQVlA4TA8AAAAvAkAAEAcQ/Y8CBiKi/wEA",
	"extended": "UklGRl4AAABXRUJQVlA4WAoAAAAQAAAAAgAAAQAAQUxQSAcAAAAAgICAgICAAFZQOCAwAAAA0AEAnQEqAwACAAFAJiWgAnS6AfgAA7AA/vLrf/zYFc1z7/f/0uD9Lg/S4P/SkAAA",
}

func TestMaterialImagesCanBeReadForPublishing(t *testing.T) {
	root := t.TempDir()
	db, err := database.Open(filepath.Join(root, "materials.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	s := &Server{db: db, cfg: config.Config{DataDir: filepath.Join(root, "custom data")}}
	ctx := context.WithValue(context.Background(), userKey{}, User{ID: 1, Role: "ADMIN"})

	for name, encoded := range publishWebPFixtures {
		t.Run(name, func(t *testing.T) {
			content, err := base64.StdEncoding.DecodeString(encoded)
			if err != nil {
				t.Fatal(err)
			}
			var body bytes.Buffer
			writer := multipart.NewWriter(&body)
			part, err := writer.CreateFormFile("files", name+".webp")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = part.Write(content); err != nil {
				t.Fatal(err)
			}
			if err = writer.Close(); err != nil {
				t.Fatal(err)
			}
			upload := httptest.NewRequest(http.MethodPost, "/", &body)
			upload.Header.Set("Content-Type", writer.FormDataContentType())
			w := httptest.NewRecorder()
			s.uploadPublishImages(w, upload)
			var uploaded struct {
				Success bool
				Data    struct{ Paths, URLs []string }
			}
			if err = json.Unmarshal(w.Body.Bytes(), &uploaded); err != nil || !uploaded.Success || len(uploaded.Data.URLs) != 1 || len(uploaded.Data.Paths) != 1 {
				t.Fatalf("upload failed: %s (%v)", w.Body.String(), err)
			}
			imageURL := uploaded.Data.URLs[0]
			material := map[string]any{
				"title": "material", "description": "description", "price": 2.5,
				"images":         []string{imageURL},
				"specifications": []any{map[string]any{"name": "颜色", "values": []any{map[string]any{"name": "红色", "image": imageURL}}}},
				"sku_rows":       []any{map[string]any{"specs": map[string]any{"颜色": "红色"}, "price": 2.5, "stock": 1}},
			}
			raw, err := json.Marshal(material)
			if err != nil {
				t.Fatal(err)
			}
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(raw)).WithContext(ctx)
			w = httptest.NewRecorder()
			s.materialCreate(w, request)
			var saved struct {
				Success bool
				Data    struct{ ID int64 }
			}
			if err = json.Unmarshal(w.Body.Bytes(), &saved); err != nil || !saved.Success {
				t.Fatalf("material save failed: %s (%v)", w.Body.String(), err)
			}
			imported, err := s.loadMaterial(request, strconv.FormatInt(saved.Data.ID, 10))
			if err != nil {
				t.Fatal(err)
			}
			_, _, specImages, _, err := buildPublishSKUs(imported)
			if err != nil || len(specImages) != 1 {
				t.Fatalf("imported specifications: %v (%v)", specImages, err)
			}
			sources := append(publishStringSlice(imported["images"]), specImages[0].Source, uploaded.Data.Paths[0])
			for _, source := range sources {
				got, filename, _, err := s.readPublishImage(ctx, source)
				if err != nil || !bytes.Equal(got, content) || filename != filepath.Base(imageURL) {
					t.Fatalf("read imported image %q: name=%q err=%v", source, filename, err)
				}
				width, height, err := publishImageDimensions(got)
				if err != nil || width != 3 || height != 2 {
					t.Fatalf("imported WebP dimensions: %dx%d (%v)", width, height, err)
				}
			}
		})
	}
}

func TestReadPublishImageSources(t *testing.T) {
	root := t.TempDir()
	s := &Server{cfg: config.Config{DataDir: root}}
	content := []byte("image content")
	for _, directory := range []string{"uploads", "static"} {
		path := filepath.Join(root, directory, "images", "test.png")
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, content, 0600); err != nil {
			t.Fatal(err)
		}
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		_, _ = w.Write(content)
	}))
	t.Cleanup(remote.Close)
	for _, source := range []string{
		"/uploads/images/test.png", `\uploads\images\test.png`,
		"/static/images/test.png", filepath.Join(root, "uploads", "images", "test.png"),
		remote.URL + "/test.png",
	} {
		got, filename, _, err := s.readPublishImage(context.Background(), source)
		if err != nil || !bytes.Equal(got, content) || filename != "test.png" {
			t.Errorf("read %q: name=%q err=%v", source, filename, err)
		}
	}
	for _, source := range []string{"/uploads/../outside.png", `/uploads/images/../../outside.png`, `/static/../outside.png`} {
		if _, _, _, err := s.readPublishImage(context.Background(), source); err == nil || !strings.Contains(err.Error(), "图片路径无效") {
			t.Errorf("traversal %q was not rejected: %v", source, err)
		}
	}
	if _, _, _, err := s.readPublishImage(context.Background(), "/uploads/images/missing.png"); err == nil || !strings.Contains(err.Error(), "图片文件不存在") {
		t.Fatalf("missing file error: %v", err)
	}
}

func TestPublishImageDimensions(t *testing.T) {
	picture := image.NewRGBA(image.Rect(0, 0, 7, 5))
	var pngContent, jpegContent, gifContent bytes.Buffer
	if err := png.Encode(&pngContent, picture); err != nil {
		t.Fatal(err)
	}
	if err := jpeg.Encode(&jpegContent, picture, nil); err != nil {
		t.Fatal(err)
	}
	if err := gif.Encode(&gifContent, picture, nil); err != nil {
		t.Fatal(err)
	}
	for format, content := range map[string][]byte{"PNG": pngContent.Bytes(), "JPEG": jpegContent.Bytes(), "GIF": gifContent.Bytes()} {
		width, height, err := publishImageDimensions(content)
		if err != nil || width != 7 || height != 5 {
			t.Fatalf("%s dimensions: %dx%d (%v)", format, width, height, err)
		}
	}
	for name, encoded := range publishWebPFixtures {
		content, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		for end := 0; end < len(content); end++ {
			if _, _, err := publishImageDimensions(content[:end]); err == nil {
				t.Fatalf("truncated %s WebP accepted at %d bytes", name, end)
			}
		}
		badChunk := bytes.Clone(content)
		copy(badChunk[16:20], []byte{0xff, 0xff, 0xff, 0xff})
		if _, _, err := publishImageDimensions(badChunk); err == nil {
			t.Fatalf("oversized %s WebP chunk accepted", name)
		}
	}
	if _, _, err := publishImageDimensions([]byte("not an image")); err == nil {
		t.Fatalf("invalid image error: %v", err)
	}
}
