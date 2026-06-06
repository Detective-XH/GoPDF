package main

import (
	"bytes"
	"context"
	"fmt"
	"os"

	"github.com/Detective-XH/gopdf"
)

func main() {
	path := "examples/read_plain_text/pdf_test.pdf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	f, r, err := pdf.Open(path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()

	var buf bytes.Buffer
	b, err := r.GetPlainText(context.Background())
	if err != nil {
		panic(err)
	}
	_, _ = buf.ReadFrom(b)
	content := buf.String()
	fmt.Println(content)
}
