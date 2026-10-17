// Package textextract pulls searchable text out of files for the full-text
// search: plain text and code, PDF, and office documents (docx/odt/xlsx/pptx).
package textextract

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
)

// MaxText is the maximum amount of text stored per file.
const MaxText = 1 << 20

// MaxInput is the largest file we try to read.
const MaxInput = 50 << 20

// Extract returns the text of the file at path. category is the file type
// category ("text", "pdf", "document", ...); ext the lower-case extension.
func Extract(path, category, ext string) string {
	fi, err := os.Stat(path)
	if err != nil || fi.Size() > MaxInput {
		return ""
	}
	var text string
	switch {
	case category == "text":
		text = readText(path)
	case ext == "pdf":
		text = readPDF(path)
	case ext == "docx":
		text = readZipXML(path, func(n string) bool { return n == "word/document.xml" })
	case ext == "xlsx":
		text = readZipXML(path, func(n string) bool { return n == "xl/sharedStrings.xml" })
	case ext == "pptx":
		text = readZipXML(path, func(n string) bool {
			return strings.HasPrefix(n, "ppt/slides/slide") && strings.HasSuffix(n, ".xml")
		})
	case ext == "odt" || ext == "ods" || ext == "odp":
		text = readZipXML(path, func(n string) bool { return n == "content.xml" })
	}
	return truncate(text)
}

func truncate(s string) string {
	if len(s) <= MaxText {
		return s
	}
	s = s[:MaxText]
	for !utf8.ValidString(s) && len(s) > 0 {
		s = s[:len(s)-1]
	}
	return s
}

func readText(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	b, _ := io.ReadAll(io.LimitReader(f, MaxText))
	if bytes.IndexByte(b, 0) >= 0 {
		return ""
	}
	return strings.ToValidUTF8(string(b), " ")
}

func readPDF(path string) (out string) {
	defer func() {
		if recover() != nil { // the PDF library panics on some broken files
			out = ""
		}
	}()
	f, r, err := pdf.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	var b strings.Builder
	for i := 1; i <= r.NumPage() && b.Len() < MaxText; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		t, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		b.WriteString(t)
		b.WriteByte('\n')
	}
	return strings.ToValidUTF8(b.String(), " ")
}

func readZipXML(path string, want func(string) bool) string {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return ""
	}
	defer zr.Close()
	var b strings.Builder
	for _, f := range zr.File {
		if !want(f.Name) || b.Len() >= MaxText {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			continue
		}
		dec := xml.NewDecoder(io.LimitReader(rc, MaxInput))
		for {
			tok, err := dec.Token()
			if err != nil {
				break
			}
			switch t := tok.(type) {
			case xml.CharData:
				b.Write(t)
			case xml.EndElement:
				switch t.Name.Local {
				case "p", "t", "si", "h":
					b.WriteByte(' ')
				}
			}
		}
		rc.Close()
		b.WriteByte('\n')
	}
	return b.String()
}
