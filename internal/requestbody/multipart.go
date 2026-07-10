package requestbody

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"mime/multipart"
	"net/textproto"
)

const maxAuditFieldBytes int64 = 1 << 20

type observedBody struct {
	source io.ReadCloser
	tee    io.Reader
	writer *io.PipeWriter
}

func (b *observedBody) Read(p []byte) (int, error) {
	n, err := b.tee.Read(p)
	if err == io.EOF {
		_ = b.writer.Close()
	} else if err != nil {
		_ = b.writer.CloseWithError(err)
	}
	return n, err
}
func (b *observedBody) Close() error {
	_ = b.writer.CloseWithError(io.ErrClosedPipe)
	return b.source.Close()
}

func ObserveMultipart(source io.ReadCloser, boundary string) (io.ReadCloser, <-chan Audit) {
	reader, writer := io.Pipe()
	result := make(chan Audit, 1)
	go func() { result <- parseMultipart(reader, boundary); close(result); _ = reader.Close() }()
	return &observedBody{source: source, tee: io.TeeReader(source, writer), writer: writer}, result
}

func parseMultipart(source io.Reader, boundary string) Audit {
	a := Audit{Fields: map[string][]string{}}
	mr := multipart.NewReader(source, boundary)
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			a.Err = err
			return a
		}
		name, fileName := part.FormName(), part.FileName()
		if fileName == "" {
			value, readErr := io.ReadAll(io.LimitReader(part, maxAuditFieldBytes+1))
			if readErr != nil {
				a.Err = readErr
				return a
			}
			if int64(len(value)) <= maxAuditFieldBytes {
				a.Fields[name] = append(a.Fields[name], string(value))
				if name == "model" {
					a.Model = string(value)
				}
				if name == "prompt" {
					a.Prompt = string(value)
				}
			}
			continue
		}
		h := sha256.New()
		size, copyErr := io.Copy(h, part)
		if copyErr != nil {
			a.Err = copyErr
			return a
		}
		a.Files = append(a.Files, FileSummary{FieldName: name, FileName: fileName, MIME: mediaType(part.Header), Size: size, SHA256: sumHex(h)})
	}
	return a
}
func mediaType(h textproto.MIMEHeader) string { return h.Get("Content-Type") }
func sumHex(h hash.Hash) string               { return hex.EncodeToString(h.Sum(nil)) }
func Boundary(contentType string) (string, error) {
	_, params, err := mimeParse(contentType)
	if err != nil {
		return "", err
	}
	b := params["boundary"]
	if b == "" {
		return "", fmt.Errorf("multipart boundary is missing")
	}
	return b, nil
}
