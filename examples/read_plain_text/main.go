package main

import (
	"bytes"
	"context"
	"fmt"

	"github.com/Detective-XH/gopdf"
)

func main() {
	pdf.DebugOn = true

	f, r, err := pdf.Open("./pdf_test.pdf")
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
