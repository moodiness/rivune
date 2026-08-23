package main

import (
	"bufio"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
)

var (
	headingPattern = regexp.MustCompile(`^#{1,6}\s+(.+?)\s*$`)
	htmlPattern    = regexp.MustCompile(`<[^>]+>`)
	linkPattern    = regexp.MustCompile(`\[[^\]]*\]\(([^)]+)\)`)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: go run scripts/ci/check-doc-links.go <markdown>...")
		os.Exit(2)
	}
	anchorCache := make(map[string]map[string]struct{})
	failed := false
	for _, source := range os.Args[1:] {
		lines, err := readLines(source)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", source, err)
			failed = true
			continue
		}
		for index, line := range lines {
			for _, match := range linkPattern.FindAllStringSubmatch(line, -1) {
				rawTarget := strings.TrimSpace(match[1])
				fields := strings.Fields(rawTarget)
				if len(fields) == 0 {
					fmt.Fprintf(os.Stderr, "%s:%d: empty local link target\n", source, index+1)
					failed = true
					continue
				}
				target := strings.Trim(fields[0], "<>")
				parsed, err := url.Parse(target)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s:%d: invalid link %s\n", source, index+1, target)
					failed = true
					continue
				}
				if parsed.Scheme != "" || parsed.Host != "" {
					continue
				}
				destination := source
				if parsed.Path != "" {
					path, err := url.PathUnescape(parsed.Path)
					if err != nil {
						fmt.Fprintf(os.Stderr, "%s:%d: invalid escaped path %s\n", source, index+1, target)
						failed = true
						continue
					}
					destination = filepath.Clean(filepath.Join(filepath.Dir(source), path))
				}
				if _, err := os.Stat(destination); err != nil {
					fmt.Fprintf(os.Stderr, "%s:%d: missing local link target %s\n", source, index+1, target)
					failed = true
					continue
				}
				if parsed.Fragment == "" || !strings.EqualFold(filepath.Ext(destination), ".md") {
					continue
				}
				fragment, err := url.PathUnescape(parsed.Fragment)
				if err != nil {
					fmt.Fprintf(os.Stderr, "%s:%d: invalid escaped heading %s\n", source, index+1, target)
					failed = true
					continue
				}
				anchors, ok := anchorCache[destination]
				if !ok {
					anchors, err = markdownAnchors(destination)
					if err != nil {
						fmt.Fprintf(os.Stderr, "%s:%d: %v\n", source, index+1, err)
						failed = true
						continue
					}
					anchorCache[destination] = anchors
				}
				if _, ok := anchors[strings.ToLower(fragment)]; !ok {
					fmt.Fprintf(os.Stderr, "%s:%d: missing heading #%s in %s\n", source, index+1, fragment, destination)
					failed = true
				}
			}
		}
	}
	if failed {
		os.Exit(1)
	}
}

func readLines(path string) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	var lines []string
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines, scanner.Err()
}

func markdownAnchors(path string) (map[string]struct{}, error) {
	lines, err := readLines(path)
	if err != nil {
		return nil, err
	}
	result := make(map[string]struct{})
	duplicates := make(map[string]int)
	for _, line := range lines {
		match := headingPattern.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		base := githubAnchor(match[1])
		count := duplicates[base]
		duplicates[base] = count + 1
		anchor := base
		if count > 0 {
			anchor = fmt.Sprintf("%s-%d", base, count)
		}
		result[anchor] = struct{}{}
	}
	return result, nil
}

func githubAnchor(value string) string {
	value = strings.ToLower(htmlPattern.ReplaceAllString(value, ""))
	var result strings.Builder
	separator := false
	for _, current := range value {
		switch {
		case unicode.IsLetter(current), unicode.IsDigit(current), current == '_':
			if separator && result.Len() > 0 {
				result.WriteByte('-')
			}
			separator = false
			result.WriteRune(current)
		case current == '-' || unicode.IsSpace(current):
			separator = true
		}
	}
	return strings.Trim(result.String(), "-")
}
