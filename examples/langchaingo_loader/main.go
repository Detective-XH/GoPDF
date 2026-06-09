package main

import (
	"fmt"
	"os"
)

func main() {
	path := "testdata/corpus/bench/synthetic-multipage.pdf"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}

	docs, err := loadDocuments(path)
	if err != nil {
		panic(err)
	}

	for _, d := range docs {
		fmt.Printf("page %v/%v label=%v confidence=%v title=%q chars=%d\n",
			d.Metadata["page"],
			d.Metadata["total_pages"],
			d.Metadata["page_label"],
			d.Metadata["extraction_confidence"],
			d.Metadata["title"],
			len(d.PageContent),
		)
	}
}
