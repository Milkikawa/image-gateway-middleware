package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"time"

	"image-gateway-middleware/internal/image"
	"image-gateway-middleware/internal/requestbody"
)

type Request struct {
	ID, Method, Path, Model, Prompt, Status, Error string
	RequestBody, RawResponse, RewrittenResponse    []byte
	UpstreamStatus, DownstreamStatus               int
	DurationMS                                     int64
	Fields                                         map[string][]string
	Files                                          []requestbody.FileSummary
	Images                                         []Image
}
type Image struct {
	ID, SourceURL, LocalPath, PublicURL, MIME, SHA256, Status, Error string
	Size                                                             int64
	Attempts                                                         []image.Attempt
}
type Service struct{ db *sql.DB }

func New(db *sql.DB) *Service { return &Service{db: db} }
func (s *Service) Save(ctx context.Context, r Request) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO requests(id,created_at,method,path,model,prompt,request_body,upstream_status,downstream_status,raw_response,rewritten_response,status,error,duration_ms) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, r.ID, time.Now().UTC().Format(time.RFC3339Nano), r.Method, r.Path, r.Model, r.Prompt, r.RequestBody, r.UpstreamStatus, r.DownstreamStatus, r.RawResponse, r.RewrittenResponse, r.Status, r.Error, r.DurationMS)
	if err != nil {
		return err
	}
	for k, values := range r.Fields {
		for _, v := range values {
			if _, err = tx.ExecContext(ctx, `INSERT INTO request_fields(request_id,name,value) VALUES(?,?,?)`, r.ID, k, v); err != nil {
				return err
			}
		}
	}
	for _, f := range r.Files {
		if _, err = tx.ExecContext(ctx, `INSERT INTO uploaded_files(request_id,field_name,file_name,mime,size,sha256) VALUES(?,?,?,?,?,?)`, r.ID, f.FieldName, f.FileName, f.MIME, f.Size, f.SHA256); err != nil {
			return err
		}
	}
	for _, im := range r.Images {
		if _, err = tx.ExecContext(ctx, `INSERT INTO images(id,request_id,source_url,local_path,public_url,mime,size,sha256,status,error,attempt_count,created_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)`, im.ID, r.ID, im.SourceURL, im.LocalPath, im.PublicURL, im.MIME, im.Size, im.SHA256, im.Status, im.Error, len(im.Attempts), time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
			return err
		}
		for _, a := range im.Attempts {
			if _, err = tx.ExecContext(ctx, `INSERT INTO download_attempts(image_id,attempt,http_status,error,duration_ms) VALUES(?,?,?,?,?)`, im.ID, a.Number, a.HTTPStatus, a.Error, a.DurationMS); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
func CleanupFiles(images []Image) {
	for _, im := range images {
		if im.LocalPath != "" {
			_ = os.Remove(im.LocalPath)
		}
	}
}
func EncodeFields(v any) string { b, _ := json.Marshal(v); return string(b) }
