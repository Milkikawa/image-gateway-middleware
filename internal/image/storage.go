package image

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
)

type Storage struct{ Images, Temp string }
type Stored struct {
	ID, Path, MIME, SHA256 string
	Size                   int64
}

func RandomID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}
func (s Storage) Save(id, contentType string, source io.Reader, max int64) (Stored, error) {
	part, err := os.CreateTemp(s.Temp, id+"-*.part")
	if err != nil {
		return Stored{}, err
	}
	partPath := part.Name()
	committed := false
	defer func() {
		_ = part.Close()
		if !committed {
			_ = os.Remove(partPath)
		}
	}()
	first := make([]byte, 512)
	n, readErr := io.ReadFull(source, first)
	if readErr != nil && readErr != io.ErrUnexpectedEOF && readErr != io.EOF {
		return Stored{}, readErr
	}
	first = first[:n]
	detected := detectImage(first, contentType)
	if detected == "" {
		return Stored{}, fmt.Errorf("downloaded content is not a supported raster image")
	}
	h := sha256.New()
	writer := io.MultiWriter(part, h)
	written, err := io.Copy(writer, io.MultiReader(bytes.NewReader(first), io.LimitReader(source, max-int64(n)+1)))
	if err != nil {
		return Stored{}, err
	}
	if written > max {
		return Stored{}, fmt.Errorf("image exceeds %d bytes", max)
	}
	if err = part.Sync(); err != nil {
		return Stored{}, err
	}
	if err = part.Close(); err != nil {
		return Stored{}, err
	}
	ext := extension(detected)
	final := filepath.Join(s.Images, id+ext)
	if err = os.Rename(partPath, final); err != nil {
		return Stored{}, err
	}
	if err = syncDir(s.Images); err != nil {
		_ = os.Remove(final)
		return Stored{}, err
	}
	committed = true
	return Stored{ID: id, Path: final, MIME: detected, Size: written, SHA256: hex.EncodeToString(h.Sum(nil))}, nil
}
func detectImage(first []byte, declared string) string {
	detected := httpDetect(first)
	if !strings.HasPrefix(detected, "image/") {
		return ""
	}
	switch detected {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
	default:
		return ""
	}
	if declared != "" {
		if mt, _, err := mime.ParseMediaType(declared); err == nil && strings.HasPrefix(mt, "image/") {
		} else {
			return ""
		}
	}
	return detected
}
func httpDetect(b []byte) string {
	if len(b) >= 8 && string(b[:8]) == "\x89PNG\r\n\x1a\n" {
		return "image/png"
	}
	if len(b) >= 3 && b[0] == 0xff && b[1] == 0xd8 && b[2] == 0xff {
		return "image/jpeg"
	}
	if len(b) >= 6 && (string(b[:6]) == "GIF87a" || string(b[:6]) == "GIF89a") {
		return "image/gif"
	}
	if len(b) >= 12 && string(b[:4]) == "RIFF" && string(b[8:12]) == "WEBP" {
		return "image/webp"
	}
	return ""
}
func extension(m string) string {
	switch m {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/gif":
		return ".gif"
	case "image/webp":
		return ".webp"
	}
	return ".img"
}
func syncDir(path string) error {
	d, err := os.Open(path)
	if err != nil {
		return err
	}
	defer d.Close()
	return d.Sync()
}
