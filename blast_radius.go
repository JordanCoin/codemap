package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"codemap/config"
	"codemap/render"
	"codemap/scanner"
)

type blastRadiusFormat string

const (
	blastRadiusFormatMarkdown blastRadiusFormat = "markdown"
	blastRadiusFormatText     blastRadiusFormat = "text"
	blastRadiusFormatJSON     blastRadiusFormat = "json"
)

var ansiSequencePattern = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)

type blastRadiusLimits struct {
	MaxTotalChars         int `json:"max_total_chars"`
	MaxChangedFiles       int `json:"max_changed_files"`
	MaxAffected           int `json:"max_affected"`
	MaxContext            int `json:"max_context"`
	MaxSnippets           int `json:"max_snippets"`
	MaxSnippetsPerChanged int `json:"max_snippets_per_changed"`
	SnippetRadius         int `json:"snippet_radius"`
	MaxSnippetChars       int `json:"max_snippet_chars"`
	MaxDiffChars          int `json:"max_diff_chars"`
	MaxDepsChars          int `json:"max_deps_chars"`
	MaxImportersChars     int `json:"max_importers_chars"`
	MaxImporterFiles      int `json:"max_importer_files"`
	MaxImportersPerFile   int `json:"max_importers_per_file"`
}

type blastRadiusDiff struct {
	Root              string               `json:"root"`
	Name              string               `json:"name,omitempty"`
	Mode              string               `json:"mode"`
	Files             []scanner.FileInfo   `json:"files"`
	DiffRef           string               `json:"diff_ref,omitempty"`
	Impact            []scanner.ImpactInfo `json:"impact,omitempty"`
	Depth             int                  `json:"depth,omitempty"`
	Only              []string             `json:"only,omitempty"`
	Exclude           []string             `json:"exclude,omitempty"`
	ChangedFilesTotal int                  `json:"changed_files_total"`
}

type blastRadiusDeps struct {
	Root              string                 `json:"root"`
	Mode              string                 `json:"mode"`
	Files             []scanner.FileAnalysis `json:"files"`
	ExternalDeps      map[string][]string    `json:"external_deps"`
	DiffRef           string                 `json:"diff_ref,omitempty"`
	ChangedFilesTotal int                    `json:"changed_files_total"`
}

type blastRadiusImporters struct {
	Root            string   `json:"root"`
	Mode            string   `json:"mode"`
	File            string   `json:"file"`
	Importers       []string `json:"importers"`
	Imports         []string `json:"imports,omitempty"`
	HubImports      []string `json:"hub_imports,omitempty"`
	ImporterCount   int      `json:"importer_count"`
	IsHub           bool     `json:"is_hub"`
	ImportersTotal  int      `json:"importers_total"`
	ImportsTotal    int      `json:"imports_total"`
	HubImportsTotal int      `json:"hub_imports_total"`
}

type blastRadiusHighest struct {
	File          string `json:"file"`
	ImporterCount int    `json:"importer_count"`
}

type blastRadiusSummary struct {
	ChangedFiles                      int                 `json:"changed_files"`
	ChangedFilesTotal                 int                 `json:"changed_files_total"`
	FilesWithDependents               int                 `json:"files_with_dependents"`
	ImpactedOutsideDiffTotal          int                 `json:"impacted_outside_diff_total"`
	ImpactedOutsideDiffShown          int                 `json:"impacted_outside_diff_shown"`
	DependencyContextOutsideDiffTotal int                 `json:"dependency_context_outside_diff_total"`
	DependencyContextOutsideDiffShown int                 `json:"dependency_context_outside_diff_shown"`
	MaxDirectDependents               int                 `json:"max_direct_dependents"`
	HighestBlastRadius                *blastRadiusHighest `json:"highest_blast_radius,omitempty"`
}

type blastRadiusRelation struct {
	Path             string `json:"path"`
	Via              string `json:"via"`
	Relation         string `json:"relation"`
	ViaIsHub         bool   `json:"via_is_hub,omitempty"`
	ViaImporterCount int    `json:"via_importer_count,omitempty"`
	IsHub            bool   `json:"is_hub,omitempty"`
}

type blastRadiusSnippet struct {
	Category    string `json:"category"`
	Path        string `json:"path"`
	Via         string `json:"via"`
	Reason      string `json:"reason"`
	MatchedTerm string `json:"matched_term"`
	MatchKind   string `json:"match_kind"`
	Language    string `json:"language"`
	Excerpt     string `json:"excerpt"`
}

type blastRadiusRenderedImporter struct {
	File string `json:"file"`
	Text string `json:"text"`
}

type blastRadiusRendered struct {
	Diff      string                        `json:"diff"`
	Deps      string                        `json:"deps"`
	Importers []blastRadiusRenderedImporter `json:"importers"`
}

type blastRadiusBundle struct {
	Root                         string                 `json:"root"`
	Ref                          string                 `json:"ref"`
	Summary                      blastRadiusSummary     `json:"summary"`
	Diff                         blastRadiusDiff        `json:"diff"`
	Deps                         blastRadiusDeps        `json:"deps"`
	Importers                    []blastRadiusImporters `json:"importers"`
	Limits                       blastRadiusLimits      `json:"limits"`
	ImpactedOutsideDiff          []blastRadiusRelation  `json:"impacted_outside_diff"`
	DependencyContextOutsideDiff []blastRadiusRelation  `json:"dependency_context_outside_diff"`
	Snippets                     []blastRadiusSnippet   `json:"snippets"`
	Rendered                     blastRadiusRendered    `json:"rendered"`
}

type blastChangedMeta struct {
	Functions []string
	Stem      string
	Dir       string
	DirBase   string
	PathNoExt string
}

type blastOutputBuilder struct {
	total     int
	remaining int
	builder   strings.Builder
}

func runBlastRadiusSubcommand(args []string) {
	limits := defaultBlastRadiusLimits()
	fs := flag.NewFlagSet("blast-radius", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	var jsonMode bool
	var markdownMode bool
	var textMode bool
	var help bool
	ref := fs.String("ref", "main", "Branch/ref to compare against")
	fs.BoolVar(&jsonMode, "json", false, "Emit a single JSON object")
	fs.BoolVar(&markdownMode, "markdown", false, "Emit Markdown output (default)")
	fs.BoolVar(&markdownMode, "md", false, "Emit Markdown output (default)")
	fs.BoolVar(&textMode, "text", false, "Emit plain text output")
	fs.BoolVar(&help, "help", false, "Show blast-radius help")
	fs.BoolVar(&help, "h", false, "Show blast-radius help")
	fs.IntVar(&limits.MaxTotalChars, "max-total-chars", limits.MaxTotalChars, "Hard cap for markdown/text output")
	fs.IntVar(&limits.MaxChangedFiles, "max-changed-files", limits.MaxChangedFiles, "Maximum changed files to include")
	fs.IntVar(&limits.MaxAffected, "max-affected", limits.MaxAffected, "Maximum affected files outside diff")
	fs.IntVar(&limits.MaxContext, "max-context", limits.MaxContext, "Maximum dependency context entries outside diff")
	fs.IntVar(&limits.MaxSnippets, "max-snippets", limits.MaxSnippets, "Maximum impact snippets")
	fs.IntVar(&limits.MaxSnippetsPerChanged, "max-snippets-per-changed", limits.MaxSnippetsPerChanged, "Maximum snippets per changed file")
	fs.IntVar(&limits.SnippetRadius, "snippet-radius", limits.SnippetRadius, "Lines of context around each snippet match")
	fs.IntVar(&limits.MaxSnippetChars, "max-snippet-chars", limits.MaxSnippetChars, "Maximum characters per snippet")
	fs.IntVar(&limits.MaxDiffChars, "max-diff-chars", limits.MaxDiffChars, "Maximum diff section characters")
	fs.IntVar(&limits.MaxDepsChars, "max-deps-chars", limits.MaxDepsChars, "Maximum dependency section characters")
	fs.IntVar(&limits.MaxImportersChars, "max-importers-chars", limits.MaxImportersChars, "Maximum importer section characters")
	fs.IntVar(&limits.MaxImporterFiles, "max-importer-files", limits.MaxImporterFiles, "Maximum changed files to render importer sections for")
	fs.IntVar(&limits.MaxImportersPerFile, "max-importers-per-file", limits.MaxImportersPerFile, "Maximum importer/import list entries per file in JSON")
	fs.Usage = func() {
		printBlastRadiusUsage(fs)
	}

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		os.Exit(2)
	}

	if help {
		fs.Usage()
		return
	}

	if fs.NArg() > 1 {
		fmt.Fprintln(os.Stderr, "Usage: codemap blast-radius [--json|--markdown|--text] [--ref <base-ref>] [path]")
		os.Exit(2)
	}

	format, err := chooseBlastRadiusFormat(jsonMode, markdownMode, textMode)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(2)
	}

	root := "."
	if fs.NArg() == 1 {
		root = fs.Arg(0)
	}

	limits = clampBlastRadiusLimits(limits)
	absRoot, cleanup, err := resolveBlastRadiusRoot(root)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error preparing root: %v\n", err)
		os.Exit(1)
	}
	defer cleanup()

	bundle, err := buildBlastRadiusBundle(absRoot, *ref, limits)
	if err != nil {
		if errors.Is(err, scanner.ErrAstGrepNotFound) {
			printAstGrepInstallHint(os.Stderr, err)
		} else {
			fmt.Fprintf(os.Stderr, "Error building blast radius: %v\n", err)
		}
		os.Exit(1)
	}

	switch format {
	case blastRadiusFormatJSON:
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(bundle)
	case blastRadiusFormatText:
		fmt.Print(renderBlastRadiusText(bundle))
	default:
		fmt.Print(renderBlastRadiusMarkdown(bundle))
	}
}

func printBlastRadiusUsage(fs *flag.FlagSet) {
	fmt.Println("codemap blast-radius - Build a compact, bounded blast-radius bundle")
	fmt.Println()
	fmt.Println("Usage:")
	fmt.Println("  codemap blast-radius [--json|--markdown|--text] [--ref <base-ref>] [path]")
	fmt.Println()
	fmt.Println("Examples:")
	fmt.Println("  codemap blast-radius --ref main .")
	fmt.Println("  codemap blast-radius --json --ref develop /path/to/repo")
	fmt.Println()
	fmt.Println("Flags:")
	fs.PrintDefaults()
	fmt.Println()
	fmt.Println("Environment overrides:")
	fmt.Println("  CODEMAP_BLAST_MAX_TOTAL_CHARS")
	fmt.Println("  CODEMAP_BLAST_MAX_CHANGED_FILES")
	fmt.Println("  CODEMAP_BLAST_MAX_AFFECTED")
	fmt.Println("  CODEMAP_BLAST_MAX_CONTEXT")
	fmt.Println("  CODEMAP_BLAST_MAX_SNIPPETS")
	fmt.Println("  CODEMAP_BLAST_MAX_SNIPPETS_PER_CHANGED")
	fmt.Println("  CODEMAP_BLAST_SNIPPET_RADIUS")
	fmt.Println("  CODEMAP_BLAST_MAX_SNIPPET_CHARS")
	fmt.Println("  CODEMAP_BLAST_MAX_DIFF_CHARS")
	fmt.Println("  CODEMAP_BLAST_MAX_DEPS_CHARS")
	fmt.Println("  CODEMAP_BLAST_MAX_IMPORTERS_CHARS")
	fmt.Println("  CODEMAP_BLAST_MAX_IMPORTER_FILES")
	fmt.Println("  CODEMAP_BLAST_MAX_IMPORTERS_PER_FILE")
}

func chooseBlastRadiusFormat(jsonMode, markdownMode, textMode bool) (blastRadiusFormat, error) {
	count := 0
	if jsonMode {
		count++
	}
	if markdownMode {
		count++
	}
	if textMode {
		count++
	}
	if count > 1 {
		return "", fmt.Errorf("choose only one of --json, --markdown, or --text")
	}
	switch {
	case jsonMode:
		return blastRadiusFormatJSON, nil
	case textMode:
		return blastRadiusFormatText, nil
	default:
		return blastRadiusFormatMarkdown, nil
	}
}

func defaultBlastRadiusLimits() blastRadiusLimits {
	return blastRadiusLimits{
		MaxTotalChars:         envInt("CODEMAP_BLAST_MAX_TOTAL_CHARS", 24000),
		MaxChangedFiles:       envInt("CODEMAP_BLAST_MAX_CHANGED_FILES", 20),
		MaxAffected:           envInt("CODEMAP_BLAST_MAX_AFFECTED", 12),
		MaxContext:            envInt("CODEMAP_BLAST_MAX_CONTEXT", 8),
		MaxSnippets:           envInt("CODEMAP_BLAST_MAX_SNIPPETS", 8),
		MaxSnippetsPerChanged: envInt("CODEMAP_BLAST_MAX_SNIPPETS_PER_CHANGED", 2),
		SnippetRadius:         envInt("CODEMAP_BLAST_SNIPPET_RADIUS", 2),
		MaxSnippetChars:       envInt("CODEMAP_BLAST_MAX_SNIPPET_CHARS", 700),
		MaxDiffChars:          envInt("CODEMAP_BLAST_MAX_DIFF_CHARS", 8000),
		MaxDepsChars:          envInt("CODEMAP_BLAST_MAX_DEPS_CHARS", 5000),
		MaxImportersChars:     envInt("CODEMAP_BLAST_MAX_IMPORTERS_CHARS", 6000),
		MaxImporterFiles:      envInt("CODEMAP_BLAST_MAX_IMPORTER_FILES", 8),
		MaxImportersPerFile:   envInt("CODEMAP_BLAST_MAX_IMPORTERS_PER_FILE", 12),
	}
}

func envInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return value
}

func clampBlastRadiusLimits(limits blastRadiusLimits) blastRadiusLimits {
	if limits.MaxTotalChars < 0 {
		limits.MaxTotalChars = 0
	}
	if limits.MaxChangedFiles < 0 {
		limits.MaxChangedFiles = 0
	}
	if limits.MaxAffected < 0 {
		limits.MaxAffected = 0
	}
	if limits.MaxContext < 0 {
		limits.MaxContext = 0
	}
	if limits.MaxSnippets < 0 {
		limits.MaxSnippets = 0
	}
	if limits.MaxSnippetsPerChanged < 0 {
		limits.MaxSnippetsPerChanged = 0
	}
	if limits.SnippetRadius < 0 {
		limits.SnippetRadius = 0
	}
	if limits.MaxSnippetChars < 0 {
		limits.MaxSnippetChars = 0
	}
	if limits.MaxDiffChars < 0 {
		limits.MaxDiffChars = 0
	}
	if limits.MaxDepsChars < 0 {
		limits.MaxDepsChars = 0
	}
	if limits.MaxImportersChars < 0 {
		limits.MaxImportersChars = 0
	}
	if limits.MaxImporterFiles < 0 {
		limits.MaxImporterFiles = 0
	}
	if limits.MaxImportersPerFile < 0 {
		limits.MaxImportersPerFile = 0
	}
	return limits
}

func resolveBlastRadiusRoot(root string) (string, func(), error) {
	cleanup := func() {}
	_, localErr := os.Stat(root)
	if isGitHubURL(root) && localErr != nil {
		repoName := extractRepoName(root)
		tempDir, err := cloneRepo(root, repoName)
		if err != nil {
			return "", cleanup, err
		}
		cleanup = func() { _ = os.RemoveAll(tempDir) }
		root = tempDir
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", cleanup, err
	}
	return absRoot, cleanup, nil
}

func buildBlastRadiusBundle(absRoot, ref string, limits blastRadiusLimits) (blastRadiusBundle, error) {
	diffProject, err := buildBlastRadiusDiffProject(absRoot, ref)
	if err != nil {
		return blastRadiusBundle{}, err
	}
	diffTotal := len(diffProject.Files)
	diffCapped := capBlastRadiusProject(diffProject, limits.MaxChangedFiles)
	changedSet := make(map[string]bool, len(diffProject.Files))
	for _, file := range diffProject.Files {
		changedSet[filepath.ToSlash(file.Path)] = true
	}

	depsProject := scanner.DepsProject{
		Root:         absRoot,
		Mode:         "deps",
		Files:        nil,
		ExternalDeps: map[string][]string{},
		DiffRef:      ref,
	}
	depsCapped := depsProject
	depsTotal := 0
	var fullReports []scanner.ImportersReport
	var jsonReports []blastRadiusImporters
	var rawImpacted []blastRadiusRelation
	var impacted []blastRadiusRelation
	var rawContext []blastRadiusRelation
	var context []blastRadiusRelation
	var snippets []blastRadiusSnippet

	if diffTotal > 0 {
		depsProject, err = buildBlastRadiusDepsProject(absRoot, ref, changedSet)
		if err != nil {
			return blastRadiusBundle{}, err
		}
		depsTotal = len(depsProject.Files)
		depsCapped = capBlastRadiusDepsProject(depsProject, limits.MaxChangedFiles)

		fg, err := scanner.BuildFileGraph(absRoot)
		if err != nil {
			return blastRadiusBundle{}, err
		}

		for _, file := range diffCapped.Files {
			report := buildImportersReportFromGraph(absRoot, file.Path, fg)
			fullReports = append(fullReports, report)
			jsonReports = append(jsonReports, capBlastRadiusImportersReport(report, limits.MaxImportersPerFile))
		}

		rawImpacted = collectBlastRadiusImpacted(fullReports, changedSet)
		rawContext = collectBlastRadiusContext(fullReports, changedSet)
		impacted = capBlastRadiusRelations(rawImpacted, limits.MaxAffected)
		context = capBlastRadiusRelations(rawContext, limits.MaxContext)
		snippets = buildBlastRadiusSnippets(absRoot, diffProject.Files, depsProject.Files, impacted, context, limits)
	}

	summary := buildBlastRadiusSummary(diffCapped.Files, diffTotal, impacted, rawImpacted, context, rawContext, fullReports)

	bundle := blastRadiusBundle{
		Root:    absRoot,
		Ref:     ref,
		Summary: summary,
		Diff: blastRadiusDiff{
			Root:              diffCapped.Root,
			Name:              diffCapped.Name,
			Mode:              diffCapped.Mode,
			Files:             diffCapped.Files,
			DiffRef:           diffCapped.DiffRef,
			Impact:            diffCapped.Impact,
			Depth:             diffCapped.Depth,
			Only:              diffCapped.Only,
			Exclude:           diffCapped.Exclude,
			ChangedFilesTotal: diffTotal,
		},
		Deps: blastRadiusDeps{
			Root:              depsCapped.Root,
			Mode:              depsCapped.Mode,
			Files:             depsCapped.Files,
			ExternalDeps:      depsCapped.ExternalDeps,
			DiffRef:           depsCapped.DiffRef,
			ChangedFilesTotal: depsTotal,
		},
		Importers:                    jsonReports,
		Limits:                       limits,
		ImpactedOutsideDiff:          impacted,
		DependencyContextOutsideDiff: context,
		Snippets:                     snippets,
	}
	bundle.Rendered = buildBlastRadiusRendered(diffCapped, depsCapped, fullReports, limits)
	return bundle, nil
}

func buildBlastRadiusDiffProject(absRoot, ref string) (scanner.Project, error) {
	diffInfo, err := scanner.GitDiffInfo(absRoot, ref)
	if err != nil {
		return scanner.Project{}, err
	}

	cfg := config.Load(absRoot)
	gitCache := scanner.NewGitIgnoreCache(absRoot)
	files, err := scanner.ScanFiles(absRoot, gitCache, cfg.Only, cfg.Exclude)
	if err != nil {
		return scanner.Project{}, err
	}
	files = scanner.FilterToChangedWithInfo(files, diffInfo)
	impact := scanner.AnalyzeImpact(absRoot, files)

	return scanner.Project{
		Root:    absRoot,
		Mode:    "tree",
		Files:   files,
		DiffRef: ref,
		Impact:  impact,
		Depth:   cfg.Depth,
		Only:    cfg.Only,
		Exclude: cfg.Exclude,
	}, nil
}

func buildBlastRadiusDepsProject(absRoot, ref string, changedSet map[string]bool) (scanner.DepsProject, error) {
	analyses, err := scanForDepsWithHint(absRoot)
	if err != nil {
		return scanner.DepsProject{}, err
	}
	analyses = scanner.FilterAnalysisToChanged(analyses, changedSet)
	return scanner.DepsProject{
		Root:         absRoot,
		Mode:         "deps",
		Files:        analyses,
		ExternalDeps: scanner.ReadExternalDeps(absRoot),
		DiffRef:      ref,
	}, nil
}

func capBlastRadiusProject(project scanner.Project, max int) scanner.Project {
	if max >= len(project.Files) {
		return project
	}
	project.Files = append([]scanner.FileInfo(nil), project.Files[:max]...)
	if len(project.Impact) > max {
		project.Impact = append([]scanner.ImpactInfo(nil), project.Impact[:max]...)
	}
	return project
}

func capBlastRadiusDepsProject(project scanner.DepsProject, max int) scanner.DepsProject {
	if max >= len(project.Files) {
		return project
	}
	project.Files = append([]scanner.FileAnalysis(nil), project.Files[:max]...)
	return project
}

func capBlastRadiusImportersReport(report scanner.ImportersReport, max int) blastRadiusImporters {
	cappedImporters := capStringSlice(report.Importers, max)
	cappedImports := capStringSlice(report.Imports, max)
	cappedHubImports := capStringSlice(report.HubImports, max)
	return blastRadiusImporters{
		Root:            report.Root,
		Mode:            report.Mode,
		File:            report.File,
		Importers:       cappedImporters,
		Imports:         cappedImports,
		HubImports:      cappedHubImports,
		ImporterCount:   report.ImporterCount,
		IsHub:           report.IsHub,
		ImportersTotal:  len(report.Importers),
		ImportsTotal:    len(report.Imports),
		HubImportsTotal: len(report.HubImports),
	}
}

func capBlastRadiusRelations(relations []blastRadiusRelation, max int) []blastRadiusRelation {
	if max >= len(relations) {
		return append([]blastRadiusRelation(nil), relations...)
	}
	return append([]blastRadiusRelation(nil), relations[:max]...)
}

func capStringSlice(items []string, max int) []string {
	if max >= len(items) {
		return append([]string(nil), items...)
	}
	return append([]string(nil), items[:max]...)
}

func collectBlastRadiusImpacted(reports []scanner.ImportersReport, changedSet map[string]bool) []blastRadiusRelation {
	var impacted []blastRadiusRelation
	seen := make(map[string]bool)
	for _, report := range reports {
		for _, importer := range report.Importers {
			importer = filepath.ToSlash(importer)
			if changedSet[importer] {
				continue
			}
			key := importer + "|" + report.File
			if seen[key] {
				continue
			}
			seen[key] = true
			impacted = append(impacted, blastRadiusRelation{
				Path:             importer,
				Via:              report.File,
				Relation:         "imports_changed_file",
				ViaIsHub:         report.IsHub,
				ViaImporterCount: report.ImporterCount,
			})
		}
	}

	sort.Slice(impacted, func(i, j int) bool {
		if impacted[i].ViaImporterCount != impacted[j].ViaImporterCount {
			return impacted[i].ViaImporterCount > impacted[j].ViaImporterCount
		}
		if impacted[i].Path != impacted[j].Path {
			return impacted[i].Path < impacted[j].Path
		}
		return impacted[i].Via < impacted[j].Via
	})
	return impacted
}

func collectBlastRadiusContext(reports []scanner.ImportersReport, changedSet map[string]bool) []blastRadiusRelation {
	var context []blastRadiusRelation
	seen := make(map[string]bool)
	for _, report := range reports {
		hubImports := make(map[string]bool, len(report.HubImports))
		for _, hub := range report.HubImports {
			hubImports[filepath.ToSlash(hub)] = true
		}
		for _, imp := range report.Imports {
			imp = filepath.ToSlash(imp)
			if changedSet[imp] {
				continue
			}
			key := imp + "|" + report.File
			if seen[key] {
				continue
			}
			seen[key] = true
			relation := "internal_dependency"
			if hubImports[imp] {
				relation = "shared_hub_dependency"
			}
			context = append(context, blastRadiusRelation{
				Path:     imp,
				Via:      report.File,
				Relation: relation,
				IsHub:    hubImports[imp],
			})
		}
	}

	sort.Slice(context, func(i, j int) bool {
		leftOrder := 1
		rightOrder := 1
		if context[i].Relation == "shared_hub_dependency" {
			leftOrder = 0
		}
		if context[j].Relation == "shared_hub_dependency" {
			rightOrder = 0
		}
		if leftOrder != rightOrder {
			return leftOrder < rightOrder
		}
		if context[i].Path != context[j].Path {
			return context[i].Path < context[j].Path
		}
		return context[i].Via < context[j].Via
	})
	return context
}

func buildBlastRadiusSummary(diffFiles []scanner.FileInfo, diffTotal int, impacted []blastRadiusRelation, rawImpacted []blastRadiusRelation, context []blastRadiusRelation, rawContext []blastRadiusRelation, reports []scanner.ImportersReport) blastRadiusSummary {
	summary := blastRadiusSummary{
		ChangedFiles:                      len(diffFiles),
		ChangedFilesTotal:                 diffTotal,
		FilesWithDependents:               countReportsWithDependents(reports),
		ImpactedOutsideDiffTotal:          uniqueRelationPaths(rawImpacted),
		ImpactedOutsideDiffShown:          uniqueRelationPaths(impacted),
		DependencyContextOutsideDiffTotal: uniqueRelationPaths(rawContext),
		DependencyContextOutsideDiffShown: uniqueRelationPaths(context),
		MaxDirectDependents:               maxDirectDependents(reports),
	}

	for _, report := range reports {
		if report.ImporterCount == 0 {
			continue
		}
		if summary.HighestBlastRadius == nil || report.ImporterCount > summary.HighestBlastRadius.ImporterCount || (report.ImporterCount == summary.HighestBlastRadius.ImporterCount && report.File < summary.HighestBlastRadius.File) {
			summary.HighestBlastRadius = &blastRadiusHighest{
				File:          report.File,
				ImporterCount: report.ImporterCount,
			}
		}
	}
	return summary
}

func countReportsWithDependents(reports []scanner.ImportersReport) int {
	count := 0
	for _, report := range reports {
		if report.ImporterCount > 0 {
			count++
		}
	}
	return count
}

func uniqueRelationPaths(items []blastRadiusRelation) int {
	seen := make(map[string]bool, len(items))
	for _, item := range items {
		seen[item.Path] = true
	}
	return len(seen)
}

func maxDirectDependents(reports []scanner.ImportersReport) int {
	max := 0
	for _, report := range reports {
		if report.ImporterCount > max {
			max = report.ImporterCount
		}
	}
	return max
}

func buildBlastRadiusSnippets(root string, diffFiles []scanner.FileInfo, depsFiles []scanner.FileAnalysis, impacted []blastRadiusRelation, context []blastRadiusRelation, limits blastRadiusLimits) []blastRadiusSnippet {
	if limits.MaxSnippets == 0 || limits.MaxSnippetsPerChanged == 0 {
		return nil
	}

	changedMeta := make(map[string]blastChangedMeta, len(diffFiles))
	for _, file := range diffFiles {
		meta := newBlastChangedMeta(file.Path)
		changedMeta[filepath.ToSlash(file.Path)] = meta
	}
	for _, file := range depsFiles {
		meta := newBlastChangedMeta(file.Path)
		meta.Functions = append([]string(nil), file.Functions...)
		changedMeta[filepath.ToSlash(file.Path)] = meta
	}

	var snippets []blastRadiusSnippet
	perChanged := make(map[string]int)

	for _, item := range impacted {
		if len(snippets) >= limits.MaxSnippets {
			break
		}
		if perChanged[item.Via] >= limits.MaxSnippetsPerChanged {
			continue
		}
		snippet := findBlastRadiusSnippet(root, item.Path, item.Via, "impacted_outside_diff", fmt.Sprintf("depends on changed file %s", item.Via), changedMeta, limits)
		if snippet == nil {
			continue
		}
		snippets = append(snippets, *snippet)
		perChanged[item.Via]++
	}

	for _, item := range context {
		if len(snippets) >= limits.MaxSnippets {
			break
		}
		if perChanged[item.Via] >= limits.MaxSnippetsPerChanged {
			continue
		}
		snippet := findBlastRadiusSnippet(root, item.Path, item.Via, "dependency_context_outside_diff", fmt.Sprintf("reachable from changed file %s", item.Via), changedMeta, limits)
		if snippet == nil {
			continue
		}
		snippets = append(snippets, *snippet)
		perChanged[item.Via]++
	}

	return snippets
}

func newBlastChangedMeta(file string) blastChangedMeta {
	file = filepath.ToSlash(file)
	stem := strings.TrimSuffix(path.Base(file), path.Ext(file))
	dir := path.Dir(file)
	if dir == "." {
		dir = ""
	}
	dirBase := ""
	if dir != "" {
		dirBase = path.Base(dir)
	}
	return blastChangedMeta{
		Stem:      stem,
		Dir:       dir,
		DirBase:   dirBase,
		PathNoExt: strings.TrimSuffix(file, path.Ext(file)),
	}
}

func findBlastRadiusSnippet(root, targetPath, via, category, reason string, changedMeta map[string]blastChangedMeta, limits blastRadiusLimits) *blastRadiusSnippet {
	absPath := filepath.Join(root, filepath.FromSlash(targetPath))
	data, err := os.ReadFile(absPath)
	if err != nil {
		return nil
	}
	content := string(data)
	if !utf8.ValidString(content) {
		content = strings.ToValidUTF8(content, "\uFFFD")
	}
	lines := strings.Split(strings.ReplaceAll(content, "\r\n", "\n"), "\n")
	if len(lines) == 0 {
		return nil
	}

	for _, term := range blastSnippetTerms(changedMeta[via]) {
		for idx, line := range lines {
			if !blastLineMatchesTerm(line, term) {
				continue
			}
			language := scanner.DetectLanguage(targetPath)
			if language == "" {
				language = "text"
			}
			return &blastRadiusSnippet{
				Category:    category,
				Path:        targetPath,
				Via:         via,
				Reason:      reason,
				MatchedTerm: term.Value,
				MatchKind:   term.Kind,
				Language:    language,
				Excerpt:     buildBlastSnippetExcerpt(lines, idx, limits.SnippetRadius, limits.MaxSnippetChars),
			}
		}
	}

	return nil
}

type blastSnippetTerm struct {
	Value string
	Kind  string
}

func blastSnippetTerms(meta blastChangedMeta) []blastSnippetTerm {
	var functions []string
	functions = append(functions, meta.Functions...)
	sort.Slice(functions, func(i, j int) bool {
		if utf8.RuneCountInString(functions[i]) != utf8.RuneCountInString(functions[j]) {
			return utf8.RuneCountInString(functions[i]) > utf8.RuneCountInString(functions[j])
		}
		return functions[i] < functions[j]
	})

	var terms []blastSnippetTerm
	seen := make(map[string]bool)
	for _, fn := range functions {
		fn = strings.TrimSpace(fn)
		if fn == "" || seen[fn] {
			continue
		}
		seen[fn] = true
		terms = append(terms, blastSnippetTerm{Value: fn, Kind: "symbol"})
	}

	for _, candidate := range []blastSnippetTerm{
		{Value: meta.PathNoExt, Kind: "path"},
		{Value: meta.Dir, Kind: "path"},
		{Value: meta.DirBase, Kind: "identifier"},
		{Value: meta.Stem, Kind: "identifier"},
	} {
		value := strings.TrimSpace(candidate.Value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		terms = append(terms, blastSnippetTerm{Value: value, Kind: candidate.Kind})
	}

	return terms
}

func blastLineMatchesTerm(line string, term blastSnippetTerm) bool {
	switch term.Kind {
	case "symbol":
		pattern := regexp.MustCompile(`\b` + regexp.QuoteMeta(term.Value) + `\b`)
		return pattern.MatchString(line)
	default:
		return strings.Contains(line, term.Value)
	}
}

func buildBlastSnippetExcerpt(lines []string, index, radius, maxChars int) string {
	start := index - radius
	if start < 0 {
		start = 0
	}
	end := index + radius + 1
	if end > len(lines) {
		end = len(lines)
	}

	var excerpt []string
	for i := start; i < end; i++ {
		excerpt = append(excerpt, fmt.Sprintf("%4d | %s", i+1, lines[i]))
	}
	text := strings.Join(excerpt, "\n")
	if runeLen(text) <= maxChars {
		return text
	}
	return truncateRunes(text, maxChars, "\n... [truncated]")
}

func buildBlastRadiusRendered(diffProject scanner.Project, depsProject scanner.DepsProject, reports []scanner.ImportersReport, limits blastRadiusLimits) blastRadiusRendered {
	rendered := blastRadiusRendered{
		Diff: truncateBlastRadiusText(renderDiffProject(diffProject), limits.MaxDiffChars, "diff"),
		Deps: truncateBlastRadiusText(renderDepsProject(depsProject), limits.MaxDepsChars, "deps"),
	}

	importerBudget := limits.MaxImportersChars
	for idx, report := range reports {
		if idx >= limits.MaxImporterFiles || importerBudget <= 0 {
			break
		}
		perFileBudget := minInt(importerBudget, 1200)
		text := truncateBlastRadiusText(renderImportersReportString(report), perFileBudget, "importers:"+report.File)
		rendered.Importers = append(rendered.Importers, blastRadiusRenderedImporter{
			File: report.File,
			Text: text,
		})
		importerBudget -= runeLen(text)
	}

	return rendered
}

func renderBlastRadiusMarkdown(bundle blastRadiusBundle) string {
	builder := newBlastOutputBuilder(bundle.Limits.MaxTotalChars)

	var summary strings.Builder
	summary.WriteString("# Codemap Blast Radius\n\n")
	summary.WriteString(fmt.Sprintf("- Root: `%s`\n", bundle.Root))
	summary.WriteString(fmt.Sprintf("- Base ref: `%s`\n\n", bundle.Ref))
	summary.WriteString("## Summary\n\n")
	summary.WriteString(fmt.Sprintf("- Changed files: %d shown of %d\n", bundle.Summary.ChangedFiles, bundle.Summary.ChangedFilesTotal))
	summary.WriteString(fmt.Sprintf("- Changed files with direct dependents: %d\n", bundle.Summary.FilesWithDependents))
	summary.WriteString(fmt.Sprintf("- Affected files outside diff: %d shown of %d\n", bundle.Summary.ImpactedOutsideDiffShown, bundle.Summary.ImpactedOutsideDiffTotal))
	summary.WriteString(fmt.Sprintf("- Dependency context outside diff: %d shown of %d\n", bundle.Summary.DependencyContextOutsideDiffShown, bundle.Summary.DependencyContextOutsideDiffTotal))
	if bundle.Summary.HighestBlastRadius != nil {
		summary.WriteString(fmt.Sprintf("- Highest blast radius: `%s` (%d direct dependents)\n", bundle.Summary.HighestBlastRadius.File, bundle.Summary.HighestBlastRadius.ImporterCount))
	}
	summary.WriteString(fmt.Sprintf("- Output budgets: total %d chars, diff %d, deps %d, importers %d\n", bundle.Limits.MaxTotalChars, bundle.Limits.MaxDiffChars, bundle.Limits.MaxDepsChars, bundle.Limits.MaxImportersChars))
	summary.WriteString(fmt.Sprintf("- Snippet limits: %d total, %d per changed file, %d chars max\n\n", bundle.Limits.MaxSnippets, bundle.Limits.MaxSnippetsPerChanged, bundle.Limits.MaxSnippetChars))
	if !builder.Append(summary.String(), "summary") {
		return builder.String()
	}

	if len(bundle.ImpactedOutsideDiff) > 0 {
		var section strings.Builder
		section.WriteString("## Affected Outside Diff\n\n")
		for _, item := range bundle.ImpactedOutsideDiff {
			section.WriteString(fmt.Sprintf("- `%s` depends on changed file `%s`", item.Path, item.Via))
			if item.ViaIsHub {
				section.WriteString(fmt.Sprintf(" [hub, %d dependents]", item.ViaImporterCount))
			}
			section.WriteString("\n")
		}
		section.WriteString("\n")
		if !builder.Append(section.String(), "affected outside diff") {
			return builder.String()
		}
	}

	if len(bundle.DependencyContextOutsideDiff) > 0 {
		var section strings.Builder
		section.WriteString("## Dependency Context Outside Diff\n\n")
		for _, item := range bundle.DependencyContextOutsideDiff {
			section.WriteString(fmt.Sprintf("- changed file `%s` reaches `%s`", item.Via, item.Path))
			if item.Relation == "shared_hub_dependency" {
				section.WriteString(" [shared hub]")
			}
			section.WriteString("\n")
		}
		section.WriteString("\n")
		if !builder.Append(section.String(), "dependency context") {
			return builder.String()
		}
	}

	if len(bundle.Snippets) > 0 {
		var section strings.Builder
		section.WriteString("## Impact Snippets\n")
		for _, snippet := range bundle.Snippets {
			section.WriteString("\n")
			section.WriteString(fmt.Sprintf("### `%s` via `%s`\n\n", snippet.Path, snippet.Via))
			section.WriteString(fmt.Sprintf("- Reason: %s\n", snippet.Reason))
			section.WriteString(fmt.Sprintf("- Match: `%s` (%s)\n\n", snippet.MatchedTerm, snippet.MatchKind))
			section.WriteString("```" + snippet.Language + "\n")
			section.WriteString(snippet.Excerpt)
			section.WriteString("\n```\n")
		}
		section.WriteString("\n")
		if !builder.Append(section.String(), "impact snippets") {
			return builder.String()
		}
	}

	diffSection := "## Diff\n\n```text\n" + bundle.Rendered.Diff + "\n```\n\n"
	if !builder.Append(diffSection, "diff section") {
		return builder.String()
	}

	depsSection := "## Dependency Flow (Changed Files)\n\n```text\n" + bundle.Rendered.Deps + "\n```\n"
	if !builder.Append(depsSection, "deps section") {
		return builder.String()
	}

	if len(bundle.Rendered.Importers) > 0 {
		var section strings.Builder
		section.WriteString("\n## Importers\n")
		for _, importer := range bundle.Rendered.Importers {
			section.WriteString(fmt.Sprintf("\n### `%s`\n\n```text\n%s\n```\n", importer.File, importer.Text))
		}
		if len(bundle.Importers) > len(bundle.Rendered.Importers) {
			section.WriteString("\n... [additional importer sections omitted]\n")
		}
		builder.Append(section.String(), "importers section")
	}

	return builder.String()
}

func renderBlastRadiusText(bundle blastRadiusBundle) string {
	builder := newBlastOutputBuilder(bundle.Limits.MaxTotalChars)

	var summary strings.Builder
	summary.WriteString("CODEMAP BLAST RADIUS\n")
	summary.WriteString(fmt.Sprintf("root=%s\n", bundle.Root))
	summary.WriteString(fmt.Sprintf("ref=%s\n\n", bundle.Ref))
	summary.WriteString("[summary]\n")
	summary.WriteString(fmt.Sprintf("changed_files=%d/%d\n", bundle.Summary.ChangedFiles, bundle.Summary.ChangedFilesTotal))
	summary.WriteString(fmt.Sprintf("files_with_dependents=%d\n", bundle.Summary.FilesWithDependents))
	summary.WriteString(fmt.Sprintf("impacted_outside_diff=%d/%d\n", bundle.Summary.ImpactedOutsideDiffShown, bundle.Summary.ImpactedOutsideDiffTotal))
	summary.WriteString(fmt.Sprintf("dependency_context_outside_diff=%d/%d\n", bundle.Summary.DependencyContextOutsideDiffShown, bundle.Summary.DependencyContextOutsideDiffTotal))
	summary.WriteString(fmt.Sprintf("output_budgets=%d_total,%d_diff,%d_deps,%d_importers\n", bundle.Limits.MaxTotalChars, bundle.Limits.MaxDiffChars, bundle.Limits.MaxDepsChars, bundle.Limits.MaxImportersChars))
	summary.WriteString(fmt.Sprintf("snippet_limits=%d_total,%d_per_changed,%d_chars\n\n", bundle.Limits.MaxSnippets, bundle.Limits.MaxSnippetsPerChanged, bundle.Limits.MaxSnippetChars))
	if !builder.Append(summary.String(), "summary") {
		return builder.String()
	}

	if len(bundle.ImpactedOutsideDiff) > 0 {
		var section strings.Builder
		section.WriteString("[affected_outside_diff]\n")
		for _, item := range bundle.ImpactedOutsideDiff {
			section.WriteString(fmt.Sprintf("%s <= %s\n", item.Path, item.Via))
		}
		section.WriteString("\n")
		if !builder.Append(section.String(), "affected outside diff") {
			return builder.String()
		}
	}

	if len(bundle.DependencyContextOutsideDiff) > 0 {
		var section strings.Builder
		section.WriteString("[dependency_context_outside_diff]\n")
		for _, item := range bundle.DependencyContextOutsideDiff {
			section.WriteString(fmt.Sprintf("%s => %s", item.Via, item.Path))
			if item.Relation == "shared_hub_dependency" {
				section.WriteString(" [shared hub]")
			}
			section.WriteString("\n")
		}
		section.WriteString("\n")
		if !builder.Append(section.String(), "dependency context") {
			return builder.String()
		}
	}

	if len(bundle.Snippets) > 0 {
		var section strings.Builder
		section.WriteString("[impact_snippets]\n")
		for _, snippet := range bundle.Snippets {
			section.WriteString(fmt.Sprintf("\n%s <= %s [%s]\n", snippet.Path, snippet.Via, snippet.MatchedTerm))
			section.WriteString(snippet.Excerpt)
			section.WriteString("\n")
		}
		section.WriteString("\n")
		if !builder.Append(section.String(), "impact snippets") {
			return builder.String()
		}
	}

	if !builder.Append("[diff]\n"+bundle.Rendered.Diff+"\n", "diff section") {
		return builder.String()
	}
	if !builder.Append("[deps]\n"+bundle.Rendered.Deps+"\n", "deps section") {
		return builder.String()
	}

	if len(bundle.Rendered.Importers) > 0 {
		var section strings.Builder
		for _, importer := range bundle.Rendered.Importers {
			section.WriteString(fmt.Sprintf("\n[importers] %s\n%s\n", importer.File, importer.Text))
		}
		if len(bundle.Importers) > len(bundle.Rendered.Importers) {
			section.WriteString("\n... [additional importer sections omitted]\n")
		}
		builder.Append(section.String(), "importers section")
	}

	return builder.String()
}

func newBlastOutputBuilder(total int) *blastOutputBuilder {
	return &blastOutputBuilder{total: total, remaining: total}
}

func (b *blastOutputBuilder) Append(text, label string) bool {
	if b.remaining <= 0 {
		return false
	}
	if runeLen(text) <= b.remaining {
		b.builder.WriteString(text)
		b.remaining -= runeLen(text)
		return true
	}
	marker := fmt.Sprintf("\n... [%s omitted after total budget %d chars]\n", label, b.total)
	keep := b.remaining - runeLen(marker)
	if keep < 0 {
		keep = 0
	}
	b.builder.WriteString(firstRunes(text, keep))
	b.builder.WriteString(marker)
	b.remaining = 0
	return false
}

func (b *blastOutputBuilder) String() string {
	return b.builder.String()
}

func truncateBlastRadiusText(text string, maxChars int, label string) string {
	if maxChars <= 0 {
		return fmt.Sprintf("... [%s omitted]\n", label)
	}
	if runeLen(text) <= maxChars {
		return text
	}
	marker := fmt.Sprintf("\n... [%s truncated to %d chars]\n", label, maxChars)
	keep := maxChars - runeLen(marker)
	if keep < 0 {
		keep = 0
	}
	return firstRunes(text, keep) + marker
}

func renderDiffProject(project scanner.Project) string {
	var buf bytes.Buffer
	render.Tree(&buf, project)
	return stripANSI(buf.String())
}

func renderDepsProject(project scanner.DepsProject) string {
	var buf bytes.Buffer
	render.Depgraph(&buf, project)
	return stripANSI(buf.String())
}

func renderImportersReportString(report scanner.ImportersReport) string {
	var buf bytes.Buffer
	renderImportersReport(&buf, report)
	return buf.String()
}

func renderImportersReport(w io.Writer, report scanner.ImportersReport) {
	if len(report.Importers) >= 3 {
		fmt.Fprintf(w, "⚠️  HUB FILE: %s\n", report.File)
		fmt.Fprintf(w, "   Imported by %d files - changes have wide impact!\n", len(report.Importers))
		fmt.Fprintln(w)
		fmt.Fprintln(w, "   Dependents:")
		for i, imp := range report.Importers {
			if i >= 5 {
				fmt.Fprintf(w, "   ... and %d more\n", len(report.Importers)-5)
				break
			}
			fmt.Fprintf(w, "   • %s\n", imp)
		}
	} else if len(report.Importers) > 0 {
		fmt.Fprintf(w, "📍 File: %s\n", report.File)
		fmt.Fprintf(w, "   Imported by %d file(s)\n", len(report.Importers))
		for _, imp := range report.Importers {
			fmt.Fprintf(w, "   • %s\n", imp)
		}
	}

	if len(report.HubImports) > 0 {
		if len(report.Importers) == 0 {
			fmt.Fprintf(w, "📍 File: %s\n", report.File)
		}
		fmt.Fprintf(w, "   Imports %d hub(s): %s\n", len(report.HubImports), strings.Join(report.HubImports, ", "))
	}
}

func buildImportersReportFromGraph(root, file string, fg *scanner.FileGraph) scanner.ImportersReport {
	if filepath.IsAbs(file) {
		if rel, err := filepath.Rel(root, file); err == nil {
			file = rel
		}
	}
	file = filepath.ToSlash(file)

	importers := append([]string(nil), fg.Importers[file]...)
	imports := append([]string(nil), fg.Imports[file]...)
	sort.Strings(importers)
	sort.Strings(imports)

	report := scanner.ImportersReport{
		Root:          root,
		Mode:          "importers",
		File:          file,
		Importers:     importers,
		Imports:       imports,
		ImporterCount: len(importers),
		IsHub:         len(importers) >= 3,
	}

	for _, imp := range imports {
		if fg.IsHub(imp) {
			report.HubImports = append(report.HubImports, imp)
		}
	}
	sort.Strings(report.HubImports)
	return report
}

func printAstGrepInstallHint(w io.Writer, err error) {
	fmt.Fprintf(w, "Error: %v\n", err)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "The --deps feature requires ast-grep. Install it with:")
	fmt.Fprintln(w, "  brew install ast-grep         # macOS/Linux (installs as 'sg')")
	fmt.Fprintln(w, "  cargo install ast-grep        # installs as 'ast-grep'")
	fmt.Fprintln(w, "  pipx install ast-grep         # installs as 'ast-grep'")
	fmt.Fprintln(w, "  python3 -m pip install ast-grep-cli")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Standard release tarballs ship codemap without the ast-grep binary.")
	fmt.Fprintln(w, "Use a codemap-full archive for self-contained CI installs, or install ast-grep separately.")
}

func stripANSI(text string) string {
	return ansiSequencePattern.ReplaceAllString(text, "")
}

func runeLen(text string) int {
	return utf8.RuneCountInString(text)
}

func firstRunes(text string, max int) string {
	if max <= 0 {
		return ""
	}
	if runeLen(text) <= max {
		return text
	}
	var builder strings.Builder
	count := 0
	for _, r := range text {
		if count >= max {
			break
		}
		builder.WriteRune(r)
		count++
	}
	return builder.String()
}

func truncateRunes(text string, max int, suffix string) string {
	if runeLen(text) <= max {
		return text
	}
	keep := max - runeLen(suffix)
	if keep < 0 {
		keep = 0
	}
	return firstRunes(text, keep) + suffix
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
