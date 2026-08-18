package scanner

import (
	"strings"
	"unicode"
)

const maxRustUseTreeDepth = 64

func expandRustUsePaths(path string) []string {
	path = strings.TrimSpace(path)
	root := path
	if end := strings.IndexAny(root, ":{ 	\r\n"); end >= 0 {
		root = root[:end]
	}
	if root != "crate" && root != "self" && root != "super" {
		return nil
	}

	paths, ok := expandRustUseTree(path, "", 0)
	if !ok {
		return nil
	}
	return dedupe(paths)
}

func expandRustUseTree(tree, prefix string, depth int) ([]string, bool) {
	if depth > maxRustUseTreeDepth {
		return nil, false
	}
	tree = strings.TrimSpace(tree)
	if tree == "" {
		return nil, false
	}

	var ok bool
	tree, ok = trimRustUseAlias(tree)
	if !ok {
		return nil, false
	}

	open := strings.IndexByte(tree, '{')
	if open < 0 {
		return expandRustUseLeaf(tree, prefix)
	}

	close, ok := matchingRustUseBrace(tree, open)
	if !ok || strings.TrimSpace(tree[close+1:]) != "" {
		return nil, false
	}
	head := strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(tree[:open]), "::"))
	if strings.ContainsAny(head, "{},") {
		return nil, false
	}
	if head != "" {
		prefix = joinRustUsePath(prefix, head)
	}
	if prefix == "" {
		return nil, false
	}

	items, ok := splitRustUseItems(tree[open+1 : close])
	if !ok {
		return nil, false
	}
	var paths []string
	for _, item := range items {
		expanded, ok := expandRustUseTree(item, prefix, depth+1)
		if !ok {
			return nil, false
		}
		paths = append(paths, expanded...)
	}
	return paths, true
}

func expandRustUseLeaf(leaf, prefix string) ([]string, bool) {
	if strings.ContainsAny(leaf, "{},") {
		return nil, false
	}
	if strings.HasSuffix(leaf, "::*") {
		leaf = strings.TrimSuffix(leaf, "::*")
	} else if leaf == "self" || leaf == "*" {
		leaf = ""
	}
	path := joinRustUsePath(prefix, leaf)
	if !validRustUsePath(path) {
		return nil, false
	}
	return []string{path}, true
}

func trimRustUseAlias(tree string) (string, bool) {
	depth := 0
	for i := 0; i < len(tree); i++ {
		switch tree[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return "", false
			}
		default:
			if depth == 0 && i+2 <= len(tree) && tree[i:i+2] == "as" &&
				(i == 0 || isRustUseSpace(tree[i-1])) &&
				(i+2 == len(tree) || isRustUseSpace(tree[i+2])) {
				path := strings.TrimSpace(tree[:i])
				alias := strings.TrimSpace(tree[i+2:])
				if path == "" || !validRustUseIdentifier(alias) {
					return "", false
				}
				return path, true
			}
		}
	}
	return tree, depth == 0
}

func matchingRustUseBrace(tree string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(tree); i++ {
		switch tree[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				return i, true
			}
			if depth < 0 {
				return 0, false
			}
		}
	}
	return 0, false
}

func splitRustUseItems(body string) ([]string, bool) {
	depth := 0
	start := 0
	var items []string
	for i := 0; i < len(body); i++ {
		switch body[i] {
		case '{':
			depth++
		case '}':
			depth--
			if depth < 0 {
				return nil, false
			}
		case ',':
			if depth == 0 {
				item := strings.TrimSpace(body[start:i])
				if item == "" {
					return nil, false
				}
				items = append(items, item)
				start = i + 1
			}
		}
	}
	if depth != 0 {
		return nil, false
	}
	if item := strings.TrimSpace(body[start:]); item != "" {
		items = append(items, item)
	} else if len(items) == 0 {
		return nil, false
	}
	return items, true
}

func joinRustUsePath(prefix, path string) string {
	path = strings.TrimSpace(strings.TrimPrefix(path, "::"))
	if prefix == "" {
		return path
	}
	if path == "" {
		return prefix
	}
	return prefix + "::" + path
}

func validRustUsePath(path string) bool {
	parts := strings.Split(path, "::")
	if len(parts) == 0 {
		return false
	}
	for _, part := range parts {
		if !validRustUseIdentifier(part) {
			return false
		}
	}
	return true
}

func validRustUseIdentifier(value string) bool {
	value = strings.TrimPrefix(value, "r#")
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if r != '_' && !unicode.IsLetter(r) {
				return false
			}
		} else if r != '_' && !unicode.IsLetter(r) && !unicode.IsDigit(r) {
			return false
		}
	}
	return true
}

func isRustUseSpace(value byte) bool {
	return value == ' ' || value == '\t' || value == '\r' || value == '\n'
}
