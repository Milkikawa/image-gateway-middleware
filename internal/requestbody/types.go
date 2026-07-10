package requestbody

type FileSummary struct {
	FieldName string
	FileName  string
	MIME      string
	Size      int64
	SHA256    string
}

type Audit struct {
	Raw    []byte
	Model  string
	Prompt string
	Fields map[string][]string
	Files  []FileSummary
	Err    error
}
