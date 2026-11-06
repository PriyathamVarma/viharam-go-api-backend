package filetype

import "testing"

func TestClassify(t *testing.T) {
	png := []byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDR")
	jpg := []byte("\xff\xd8\xff\xe0\x00\x10JFIF")
	pdf := []byte("%PDF-1.7\n%....")
	webp := []byte("RIFF\x24\x00\x00\x00WEBPVP8 ")
	for name, tc := range map[string]struct {
		in   []byte
		want string
	}{"png": {png, ".png"}, "jpg": {jpg, ".jpg"}, "pdf": {pdf, ".pdf"}, "webp": {webp, ".webp"}} {
		k, ok := Classify(tc.in)
		if !ok || k.Ext != tc.want {
			t.Errorf("%s: got %+v ok=%v", name, k, ok)
		}
	}
	rejected := map[string][]byte{
		"html":                       []byte("<!DOCTYPE html><html><script>alert(1)</script>"),
		"svg":                        []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		"text":                       []byte("hello world"),
		"exe":                        []byte("MZ\x90\x00\x03\x00"),
		"gif":                        []byte("GIF89a\x01\x00"),
		"empty":                      {},
		"disguised-html-as-jpg-name": []byte("<html><body>x</body></html>"),
	}
	for name, in := range rejected {
		if _, ok := Classify(in); ok {
			t.Errorf("%s should be rejected", name)
		}
	}
}
