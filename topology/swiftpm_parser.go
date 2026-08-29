package topology

import (
	"fmt"
	"path/filepath"
	"strings"
)

type swiftPMManifest struct {
	manifest string
	root     string
	targets  []swiftPMTarget
	products []swiftPMProduct
	issues   []Issue
}

type swiftPMTarget struct {
	name         string
	kind         NodeKind
	root         string
	sourceRoots  []string
	testRoots    []string
	memberRoots  []string
	excludes     []string
	dependencies []swiftPMDependency
}

type swiftPMProduct struct {
	name    string
	targets []string
}

type swiftPMDependency struct {
	kind        string
	name        string
	packageName string
	conditional bool
}

func parseSwiftPMManifest(manifest string, data []byte) swiftPMManifest {
	parsed := swiftPMManifest{
		manifest: filepath.Clean(manifest),
		root:     filepath.Dir(filepath.Clean(manifest)),
	}
	text := stripSwiftComments(string(data))
	packageBody, ok := swiftCallBody(text, "Package")
	if !ok {
		parsed.issues = append(parsed.issues, swiftPMIssue(manifest, "computed-swiftpm-package", "Package declaration is not a literal call"))
		return parsed
	}
	targetsText, ok := swiftNamedArray(packageBody, "targets")
	if !ok {
		parsed.issues = append(parsed.issues, swiftPMIssue(manifest, "computed-swiftpm-targets", "targets is not a literal array"))
	} else {
		for _, element := range splitSwiftTopLevel(targetsText) {
			target, issue := parseSwiftPMTarget(parsed.root, element)
			if issue != nil {
				issue.Provider = "swiftpm"
				issue.Message = manifest + ": " + issue.Message
				parsed.issues = append(parsed.issues, *issue)
				continue
			}
			if target.name != "" {
				parsed.targets = append(parsed.targets, target)
			}
		}
	}
	if productsText, ok := swiftNamedArray(packageBody, "products"); ok {
		for _, element := range splitSwiftTopLevel(productsText) {
			product, issue := parseSwiftPMProduct(element)
			if issue != nil {
				issue.Provider = "swiftpm"
				issue.Message = manifest + ": " + issue.Message
				parsed.issues = append(parsed.issues, *issue)
				continue
			}
			if product.name != "" {
				parsed.products = append(parsed.products, product)
			}
		}
	}
	return parsed
}

func parseSwiftPMTarget(packageRoot, element string) (swiftPMTarget, *Issue) {
	call, body, ok := swiftElementCall(element)
	kinds := map[string]NodeKind{
		"target":           "swift-target",
		"executableTarget": "swift-executable",
		"testTarget":       "swift-test",
		"macro":            "swift-macro",
		"plugin":           "swift-plugin",
		"systemLibrary":    "swift-system-library",
		"binaryTarget":     "swift-binary",
	}
	kind, supported := kinds[call]
	if !ok || !supported {
		return swiftPMTarget{}, &Issue{Code: "computed-swiftpm-target", Message: "target declaration is not a supported literal call"}
	}
	arguments := swiftNamedArguments(body)
	name, ok := swiftLiteralString(arguments["name"])
	if !ok {
		return swiftPMTarget{}, &Issue{Code: "computed-swiftpm-target-name", Message: "target name is not a literal string"}
	}
	target := swiftPMTarget{name: name, kind: kind}
	explicitPath, hasPath := swiftLiteralString(arguments["path"])
	if arguments["path"] != "" && !hasPath {
		return swiftPMTarget{}, &Issue{Code: "computed-swiftpm-target-path", Message: fmt.Sprintf("target %q path is not a literal string", name)}
	}
	if hasPath {
		target.root = filepath.Clean(filepath.Join(packageRoot, filepath.FromSlash(explicitPath)))
	} else {
		target.root = swiftPMConventionalRoot(packageRoot, call, name)
	}

	sources, sourcesOK := swiftLiteralStringArray(arguments["sources"])
	if arguments["sources"] != "" && !sourcesOK {
		return swiftPMTarget{}, &Issue{Code: "computed-swiftpm-sources", Message: fmt.Sprintf("target %q sources is not a literal string array", name)}
	}
	excludes, excludesOK := swiftLiteralStringArray(arguments["exclude"])
	if arguments["exclude"] != "" && !excludesOK {
		return swiftPMTarget{}, &Issue{Code: "computed-swiftpm-excludes", Message: fmt.Sprintf("target %q exclude is not a literal string array", name)}
	}
	for _, exclude := range excludes {
		target.excludes = append(target.excludes, filepath.Clean(filepath.Join(target.root, filepath.FromSlash(exclude))))
	}
	if len(sources) == 0 {
		target.memberRoots = []string{target.root}
	} else {
		for _, source := range sources {
			target.memberRoots = append(target.memberRoots, filepath.Clean(filepath.Join(target.root, filepath.FromSlash(source))))
		}
	}
	switch call {
	case "testTarget":
		target.testRoots = append([]string(nil), target.memberRoots...)
	case "binaryTarget":
	default:
		target.sourceRoots = append([]string(nil), target.memberRoots...)
	}

	dependencies, dependenciesOK := swiftNamedArray(body, "dependencies")
	if arguments["dependencies"] != "" && !dependenciesOK {
		return swiftPMTarget{}, &Issue{Code: "computed-swiftpm-dependencies", Message: fmt.Sprintf("target %q dependencies is not a literal array", name)}
	}
	if dependenciesOK {
		for _, dependencyText := range splitSwiftTopLevel(dependencies) {
			dependency, issue := parseSwiftPMDependency(dependencyText)
			if issue != nil {
				return swiftPMTarget{}, issue
			}
			target.dependencies = append(target.dependencies, dependency)
		}
	}
	return target, nil
}

func parseSwiftPMProduct(element string) (swiftPMProduct, *Issue) {
	call, body, ok := swiftElementCall(element)
	if !ok || (call != "library" && call != "executable") {
		return swiftPMProduct{}, &Issue{Code: "computed-swiftpm-product", Message: "product declaration is not a supported literal call"}
	}
	arguments := swiftNamedArguments(body)
	name, ok := swiftLiteralString(arguments["name"])
	if !ok {
		return swiftPMProduct{}, &Issue{Code: "computed-swiftpm-product-name", Message: "product name is not a literal string"}
	}
	targets, ok := swiftLiteralStringArray(arguments["targets"])
	if !ok {
		return swiftPMProduct{}, &Issue{Code: "computed-swiftpm-product-targets", Message: fmt.Sprintf("product %q targets is not a literal string array", name)}
	}
	return swiftPMProduct{name: name, targets: targets}, nil
}

func parseSwiftPMDependency(text string) (swiftPMDependency, *Issue) {
	if name, ok := swiftLiteralString(text); ok {
		return swiftPMDependency{kind: "byName", name: name}, nil
	}
	call, body, ok := swiftElementCall(text)
	if !ok || (call != "target" && call != "byName" && call != "product") {
		return swiftPMDependency{}, &Issue{Code: "computed-swiftpm-dependency", Message: "target dependency is not a supported literal form"}
	}
	arguments := swiftNamedArguments(body)
	name, ok := swiftLiteralString(arguments["name"])
	if !ok {
		return swiftPMDependency{}, &Issue{Code: "computed-swiftpm-dependency-name", Message: "target dependency name is not a literal string"}
	}
	packageName, _ := swiftLiteralString(arguments["package"])
	return swiftPMDependency{
		kind:        call,
		name:        name,
		packageName: packageName,
		conditional: strings.TrimSpace(arguments["condition"]) != "",
	}, nil
}

func swiftCallBody(text, name string) (string, bool) {
	for index := 0; index < len(text); index++ {
		if end, recognized := swiftStringEnd(text, index); recognized {
			index = end - 1
			continue
		}
		if text[index] != name[0] || !strings.HasPrefix(text[index:], name) {
			continue
		}
		end := index + len(name)
		if (index == 0 || !isSwiftIdentifierByte(text[index-1])) &&
			(end == len(text) || !isSwiftIdentifierByte(text[end])) {
			cursor := skipSwiftSpace(text, end)
			if cursor < len(text) && text[cursor] == '(' {
				if body, _, ok := swiftBalancedBody(text, cursor, '(', ')'); ok {
					return body, true
				}
			}
		}
	}
	return "", false
}

func swiftNamedArray(body, name string) (string, bool) {
	value := swiftNamedArguments(body)[name]
	value = strings.TrimSpace(value)
	if value == "" || value[0] != '[' {
		return "", false
	}
	array, end, ok := swiftBalancedBody(value, 0, '[', ']')
	return array, ok && strings.TrimSpace(value[end:]) == ""
}

func swiftNamedArguments(body string) map[string]string {
	arguments := make(map[string]string)
	for _, part := range splitSwiftTopLevel(body) {
		if index := swiftTopLevelColon(part); index >= 0 {
			arguments[strings.TrimSpace(part[:index])] = strings.TrimSpace(part[index+1:])
		}
	}
	return arguments
}

func swiftElementCall(text string) (string, string, bool) {
	text = strings.TrimSpace(text)
	if !strings.HasPrefix(text, ".") {
		return "", "", false
	}
	cursor := 1
	for cursor < len(text) && isSwiftIdentifierByte(text[cursor]) {
		cursor++
	}
	name := text[1:cursor]
	cursor = skipSwiftSpace(text, cursor)
	if name == "" || cursor >= len(text) || text[cursor] != '(' {
		return "", "", false
	}
	body, end, ok := swiftBalancedBody(text, cursor, '(', ')')
	return name, body, ok && strings.TrimSpace(text[end:]) == ""
}

func splitSwiftTopLevel(text string) []string {
	var parts []string
	start := 0
	var parentheses, brackets, braces int
	for index := 0; index < len(text); index++ {
		char := text[index]
		if end, recognized := swiftStringEnd(text, index); recognized {
			index = end - 1
			continue
		}
		switch char {
		case '(':
			parentheses++
		case ')':
			parentheses--
		case '[':
			brackets++
		case ']':
			brackets--
		case '{':
			braces++
		case '}':
			braces--
		case ',':
			if parentheses == 0 && brackets == 0 && braces == 0 {
				if part := strings.TrimSpace(text[start:index]); part != "" {
					parts = append(parts, part)
				}
				start = index + 1
			}
		}
	}
	if part := strings.TrimSpace(text[start:]); part != "" {
		parts = append(parts, part)
	}
	return parts
}

func swiftTopLevelColon(text string) int {
	var parentheses, brackets, braces int
	for index := 0; index < len(text); index++ {
		if end, recognized := swiftStringEnd(text, index); recognized {
			index = end - 1
			continue
		}
		char := text[index]
		switch char {
		case '(':
			parentheses++
		case ')':
			parentheses--
		case '[':
			brackets++
		case ']':
			brackets--
		case '{':
			braces++
		case '}':
			braces--
		case ':':
			if parentheses == 0 && brackets == 0 && braces == 0 {
				return index
			}
		}
	}
	return -1
}

func swiftBalancedBody(text string, start int, open, close byte) (string, int, bool) {
	depth := 0
	for index := start; index < len(text); index++ {
		if end, recognized := swiftStringEnd(text, index); recognized {
			index = end - 1
			continue
		}
		char := text[index]
		if char == open {
			depth++
		} else if char == close {
			depth--
			if depth == 0 {
				return text[start+1 : index], index + 1, true
			}
		}
	}
	return "", start, false
}

func swiftLiteralString(text string) (string, bool) {
	text = strings.TrimSpace(text)
	valueStart, valueEnd, end, ok := swiftStringInfo(text, 0)
	if !ok || end != len(text) || swiftHasInterpolation(text[valueStart:valueEnd]) {
		return "", false
	}
	value := text[valueStart:valueEnd]
	if text[0] == '"' {
		value = strings.ReplaceAll(value, `\"`, `"`)
		value = strings.ReplaceAll(value, `\\`, `\`)
	}
	return value, true
}

func swiftLiteralStringArray(text string) ([]string, bool) {
	text = strings.TrimSpace(text)
	if len(text) < 2 || text[0] != '[' {
		return nil, false
	}
	body, end, ok := swiftBalancedBody(text, 0, '[', ']')
	if !ok || strings.TrimSpace(text[end:]) != "" {
		return nil, false
	}
	var values []string
	for _, part := range splitSwiftTopLevel(body) {
		value, ok := swiftLiteralString(part)
		if !ok {
			return nil, false
		}
		values = append(values, value)
	}
	return values, true
}

func stripSwiftComments(text string) string {
	var result strings.Builder
	lineComment, blockComment := false, false
	for index := 0; index < len(text); index++ {
		char := text[index]
		next := byte(0)
		if index+1 < len(text) {
			next = text[index+1]
		}
		switch {
		case lineComment:
			if char == '\n' {
				lineComment = false
				result.WriteByte(char)
			} else {
				result.WriteByte(' ')
			}
		case blockComment:
			if char == '*' && next == '/' {
				result.WriteString("  ")
				index++
				blockComment = false
			} else if char == '\n' {
				result.WriteByte('\n')
			} else {
				result.WriteByte(' ')
			}
		case char == '/' && next == '/':
			result.WriteString("  ")
			index++
			lineComment = true
		case char == '/' && next == '*':
			result.WriteString("  ")
			index++
			blockComment = true
		default:
			if end, recognized := swiftStringEnd(text, index); recognized {
				result.WriteString(text[index:end])
				index = end - 1
				continue
			}
			result.WriteByte(char)
		}
	}
	return result.String()
}

// swiftStringInfo recognizes normal, raw, and multiline Swift string literals.
// The parser only needs their boundaries so punctuation inside a literal cannot
// change package structure while still rejecting interpolated values below.
func swiftStringInfo(text string, start int) (valueStart, valueEnd, end int, ok bool) {
	quote, hashes, multiline, recognized := swiftStringStart(text, start)
	if !recognized {
		return 0, 0, start, false
	}
	valueStart = quote + 1
	close := `"` + strings.Repeat("#", hashes)
	if multiline {
		valueStart = quote + 3
		close = `"""` + strings.Repeat("#", hashes)
	}
	closeOffset := strings.Index(text[valueStart:], close)
	if closeOffset < 0 {
		return valueStart, len(text), len(text), false
	}
	valueEnd = valueStart + closeOffset
	return valueStart, valueEnd, valueEnd + len(close), true
}

func swiftStringEnd(text string, start int) (int, bool) {
	_, _, end, ok := swiftStringInfo(text, start)
	if end == start {
		return start, false
	}
	return end, ok || end == len(text)
}

func swiftStringStart(text string, start int) (quote, hashes int, multiline, ok bool) {
	if start >= len(text) {
		return 0, 0, false, false
	}
	quote = start
	if text[start] == '#' {
		quote = start
		for quote < len(text) && text[quote] == '#' {
			quote++
		}
		hashes = quote - start
		if quote >= len(text) || text[quote] != '"' {
			return 0, 0, false, false
		}
	} else if text[start] != '"' {
		return 0, 0, false, false
	}
	multiline = quote+2 < len(text) && text[quote:quote+3] == `"""`
	return quote, hashes, multiline, true
}

func swiftHasInterpolation(value string) bool {
	for index := 0; index < len(value); index++ {
		if value[index] != '\\' {
			continue
		}
		index++
		for index < len(value) && value[index] == '#' {
			index++
		}
		if index < len(value) && value[index] == '(' {
			return true
		}
	}
	return false
}

func swiftPMConventionalRoot(packageRoot, call, name string) string {
	base := "Sources"
	if call == "testTarget" {
		base = "Tests"
	} else if call == "plugin" {
		base = "Plugins"
	}
	return filepath.Clean(filepath.Join(packageRoot, base, name))
}

func swiftPMIssue(manifest, code, message string) Issue {
	return Issue{Provider: "swiftpm", Code: code, Message: manifest + ": " + message}
}

func isSwiftIdentifierByte(char byte) bool {
	return char == '_' || char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9'
}

func skipSwiftSpace(text string, cursor int) int {
	for cursor < len(text) && (text[cursor] == ' ' || text[cursor] == '\t' || text[cursor] == '\n' || text[cursor] == '\r') {
		cursor++
	}
	return cursor
}
