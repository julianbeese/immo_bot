// parser-replay loads previously-captured IS24 expose HTML files and reports
// what the parser extracts from each. Use to validate parser changes against
// real-world pages without restarting the bot:
//
//	go run ./cmd/parser-replay /path/to/data/debug
//
// Filenames are expected to be is24_expose_<id>.html (the DEBUG_HTML=1 dump
// format). The exit code is 0 on success regardless of parse content.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/julianbeese/immo_bot/internal/scraper/is24"
)

var idFromName = regexp.MustCompile(`is24_expose_(\d+)\.html$`)

func main() {
	dir := "data/debug"
	if len(os.Args) > 1 {
		dir = os.Args[1]
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "read dir %s: %v\n", dir, err)
		os.Exit(1)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".html") && strings.HasPrefix(e.Name(), "is24_expose_") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)

	parser := is24.NewParser()

	fmt.Printf("%-12s %-7s %-6s %-5s %-3s %-3s %-3s %-4s %s\n",
		"is24_id", "price", "rooms", "area", "bal", "ebk", "exc", "desc", "title")
	fmt.Println(strings.Repeat("-", 100))

	var total, withTitle, withPrice, withRooms, withDesc int
	for _, name := range files {
		m := idFromName.FindStringSubmatch(name)
		if len(m) < 2 {
			continue
		}
		id := m[1]
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s: %v\n", name, err)
			continue
		}
		l, _ := parser.ParseExpose(body, id)
		total++
		if l.Title != "" {
			withTitle++
		}
		if l.Price > 0 {
			withPrice++
		}
		if l.Rooms > 0 {
			withRooms++
		}
		if l.Description != "" {
			withDesc++
		}
		title := l.Title
		if len(title) > 60 {
			title = title[:57] + "..."
		}
		fmt.Printf("%-12s %-7d %-6.1f %-5d %-3s %-3s %-3s %-4d %s\n",
			id, l.Price, l.Rooms, l.Area,
			yn(l.HasBalcony), yn(l.HasEBK), yn(l.ExclusiveExpose),
			len(l.Description), title)
	}

	fmt.Println(strings.Repeat("-", 100))
	fmt.Printf("parsed=%d  title=%d  price=%d  rooms=%d  desc=%d\n",
		total, withTitle, withPrice, withRooms, withDesc)
}

func yn(b bool) string {
	if b {
		return "y"
	}
	return "-"
}
