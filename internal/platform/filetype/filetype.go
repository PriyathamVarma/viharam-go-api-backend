// Package filetype identifies uploaded files from their content, never from
// the client-supplied name or Content-Type header.
package filetype

import "net/http"

// Kind describes an accepted upload type.
type Kind struct {
	MIME string
	Ext  string
}

var allowed = map[string]Kind{
	"image/jpeg":      {MIME: "image/jpeg", Ext: ".jpg"},
	"image/png":       {MIME: "image/png", Ext: ".png"},
	"image/webp":      {MIME: "image/webp", Ext: ".webp"},
	"application/pdf": {MIME: "application/pdf", Ext: ".pdf"},
}

// Classify sniffs the leading bytes (at most 512 are inspected) and returns the
// accepted Kind, or false when the content is not an allowed image or PDF.
func Classify(head []byte) (Kind, bool) {
	if len(head) > 512 {
		head = head[:512]
	}
	k, ok := allowed[http.DetectContentType(head)]
	return k, ok
}

// Allowed lists the accepted MIME types for error messages and docs.
func Allowed() []string {
	return []string{"image/jpeg", "image/png", "image/webp", "application/pdf"}
}
