package main

import (
	"context"
	"fmt"
	"os"

	"github.com/Detective-XH/gopdf"
)

func main() {
	path := "examples/read_text_with_styles/pdf_test.pdf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	f, r, err := pdf.Open(path)
	if err != nil {
		panic(err)
	}
	defer func() { _ = f.Close() }()

	sentences, err := r.GetStyledTexts(context.Background())
	if err != nil {
		panic(err)
	}

	// Print all sentences
	for _, sentence := range sentences {
		fmt.Printf("Font: %s, Font-size: %f, x: %f, y: %f, content: %s \n",
			sentence.Font,
			sentence.FontSize,
			sentence.X,
			sentence.Y,
			sentence.S)
	}
}
