package render

import (
	"fmt"
	"io"
	"math"
	"math/rand/v2"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codemap/scanner"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"
)

// rng is a reproducible random generator for consistent skyline layouts
var rng = rand.New(rand.NewPCG(42, 0))

// Code extensions for skyline (what counts as "source code")
var codeExtensions = map[string]bool{
	".py": true, ".js": true, ".ts": true, ".jsx": true, ".tsx": true, ".go": true, ".rs": true, ".rb": true, ".java": true,
	".swift": true, ".kt": true, ".scala": true, ".c": true, ".cpp": true, ".h": true, ".hpp": true, ".cs": true, ".fs": true,
	".php": true, ".lua": true, ".r": true, ".dart": true, ".vue": true, ".svelte": true, ".elm": true, ".ex": true, ".exs": true,
	".hs": true, ".ml": true, ".clj": true, ".erl": true, ".sh": true, ".bash": true, ".zsh": true, ".fish": true, ".ps1": true,
	".html": true, ".css": true, ".scss": true, ".sass": true, ".less": true,
	".sql": true, ".graphql": true, ".proto": true,
}

var codeFilenames = map[string]bool{
	"Makefile": true, "Dockerfile": true, "Rakefile": true, "Gemfile": true, "Procfile": true,
	"Vagrantfile": true, "Jenkinsfile": true, "Fastfile": true,
}

// Building dimensions
const (
	buildingWidth = 7
	maxHeight     = 12
	minHeight     = 2
	skyHeight     = 6
)

// Building colors
var buildingColors = []string{
	"\033[36m", // cyan
	"\033[33m", // yellow
	"\033[35m", // magenta
	"\033[31m", // red
	"\033[32m", // green
	"\033[34m", // blue
	"\033[96m", // bright cyan
	"\033[93m", // bright yellow
	"\033[95m", // bright magenta
	"\033[91m", // bright red
	"\033[92m", // bright green
	"\033[94m", // bright blue
}

// Building data
type building struct {
	height   int
	char     rune
	color    string
	ext      string
	extLabel string
	count    int
	size     int64
	gap      int
}

// Aggregated extension data
type extAgg struct {
	ext   string
	size  int64
	count int
}

// filterCodeFiles returns only source code files
func filterCodeFiles(files []scanner.FileInfo) []scanner.FileInfo {
	var result []scanner.FileInfo
	for _, f := range files {
		if codeExtensions[strings.ToLower(f.Ext)] || codeFilenames[filepath.Base(f.Path)] {
			result = append(result, f)
		}
	}
	if len(result) == 0 {
		return files
	}
	return result
}

// aggregateByExtension groups files by extension
func aggregateByExtension(files []scanner.FileInfo) []extAgg {
	groups := make(map[string]*extAgg)
	for _, f := range files {
		ext := strings.ToLower(f.Ext)
		if ext == "" {
			ext = filepath.Base(f.Path)
		}
		if groups[ext] == nil {
			groups[ext] = &extAgg{ext: ext}
		}
		groups[ext].size += f.Size
		groups[ext].count++
	}

	var result []extAgg
	for _, agg := range groups {
		result = append(result, *agg)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].size > result[j].size
	})
	return result
}

// getBuildingChar returns building texture character
func getBuildingChar(ext string) rune {
	ext = strings.ToLower(ext)
	switch {
	case ext == ".go" || ext == ".dart":
		return '▓'
	case ext == ".py" || ext == ".js" || ext == ".ts" || ext == ".jsx" || ext == ".tsx":
		return '░'
	case ext == ".rb" || ext == ".erb":
		return '▒'
	case ext == ".sh" || ext == "makefile" || ext == "dockerfile":
		return '█'
	default:
		return '▓'
	}
}

// createBuildings creates building data from aggregated files
func createBuildings(sorted []extAgg, width int) []building {
	if len(sorted) == 0 {
		return nil
	}

	// Find size range
	var minSize, maxSize int64 = sorted[len(sorted)-1].size, sorted[0].size
	sizeRange := maxSize - minSize
	if sizeRange == 0 {
		sizeRange = 1
	}

	getHeight := func(size int64) int {
		ratio := float64(size-minSize) / float64(sizeRange)
		ratio = math.Sqrt(ratio)
		return minHeight + int(ratio*float64(maxHeight-minHeight))
	}

	var buildings []building

	for idx, agg := range sorted {
		extLabel := agg.ext
		if strings.HasPrefix(extLabel, ".") && len(extLabel) > 5 {
			extLabel = extLabel[:5]
		}

		buildings = append(buildings, building{
			height:   getHeight(agg.size),
			char:     getBuildingChar(agg.ext),
			color:    buildingColors[idx%len(buildingColors)],
			ext:      agg.ext,
			extLabel: extLabel,
			count:    agg.count,
			size:     agg.size,
			gap:      []int{1, 2, 2, 3}[rng.IntN(4)],
		})
	}

	// Arrange: tallest in middle
	sort.Slice(buildings, func(i, j int) bool {
		return buildings[i].height > buildings[j].height
	})

	var arranged []building
	for i, b := range buildings {
		if i%2 == 0 {
			arranged = append(arranged, b)
		} else {
			arranged = append([]building{b}, arranged...)
		}
	}

	// Limit to fit width
	totalWidth := 0
	for _, b := range arranged {
		totalWidth += buildingWidth + b.gap
	}
	for totalWidth > width-8 && len(arranged) > 0 {
		if len(arranged)%2 == 0 {
			arranged = arranged[1:]
		} else {
			arranged = arranged[:len(arranged)-1]
		}
		totalWidth = 0
		for _, b := range arranged {
			totalWidth += buildingWidth + b.gap
		}
	}

	return arranged
}

// Skyline renders the city skyline visualization to the given writer
func Skyline(w io.Writer, project scanner.Project, animate bool) {
	files := project.Files
	projectName := project.Name
	if projectName == "" {
		projectName = filepath.Base(project.Root)
	}

	width, _, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil || width <= 0 {
		width = 80
	}

	codeFiles := filterCodeFiles(files)
	sorted := aggregateByExtension(codeFiles)
	arranged := createBuildings(sorted, width)

	if len(arranged) == 0 {
		fmt.Fprintln(w, Dim+"No source files to display"+Reset)
		return
	}

	// Calculate layout
	totalWidth := 0
	for _, b := range arranged {
		totalWidth += buildingWidth + b.gap
	}
	leftMargin := (width - totalWidth) / 2
	scenePadding := 4
	sceneLeft := max(0, leftMargin-scenePadding)
	sceneRight := min(width, leftMargin+totalWidth+scenePadding)
	sceneWidth := sceneRight - sceneLeft

	// If writer is not os.Stdout, disable animation
	if animate && w == os.Stdout {
		renderAnimated(w, arranged, width, leftMargin, sceneLeft, sceneRight, sceneWidth, codeFiles, projectName, sorted)
	} else {
		renderStatic(w, arranged, width, leftMargin, sceneLeft, sceneRight, sceneWidth, codeFiles, projectName, sorted)
	}
}

// renderStatic renders static skyline to the given writer
func renderStatic(w io.Writer, arranged []building, width, leftMargin, sceneLeft, sceneRight, sceneWidth int,
	codeFiles []scanner.FileInfo, projectName string, sorted []extAgg) {
	// Build grid
	grid := make([][]rune, skyHeight+maxHeight+1)
	for i := range grid {
		grid[i] = make([]rune, width)
		for j := range grid[i] {
			grid[i][j] = ' '
		}
	}

	// Render sky
	for row := 0; row < skyHeight; row++ {
		for i := 0; i < sceneWidth/10; i++ {
			col := rng.IntN(sceneRight-sceneLeft) + sceneLeft
			if col >= 0 && col < width {
				stars := []rune{'·', '·', '·', '✦', '*', '·'}
				grid[row][col] = stars[rng.IntN(len(stars))]
			}
		}
	}

	// Moon
	moonCol := sceneRight - 3
	if moonCol >= 0 && moonCol < width {
		grid[1][moonCol] = '◐'
	}

	// Render buildings
	col := leftMargin
	for _, b := range arranged {
		buildingTop := skyHeight + maxHeight - b.height

		// Rooftop cap
		if buildingTop > skyHeight {
			for j := 0; j < buildingWidth; j++ {
				if col+j < width {
					grid[buildingTop][col+j] = '▄'
				}
			}
		}

		// Building body
		buildingHeight := skyHeight + maxHeight + 1 - buildingTop - 1
		centerRow := buildingTop + 1 + buildingHeight/2

		for row := buildingTop + 1; row < skyHeight+maxHeight+1; row++ {
			for j := 0; j < buildingWidth; j++ {
				if col+j < width {
					if row == centerRow && buildingHeight >= 3 {
						extStart := (buildingWidth - len(b.extLabel)) / 2
						extEnd := extStart + len(b.extLabel)
						if j >= extStart && j < extEnd {
							grid[row][col+j] = rune(b.extLabel[j-extStart])
						} else {
							grid[row][col+j] = b.char
						}
					} else {
						grid[row][col+j] = b.char
					}
				}
			}
		}
		col += buildingWidth + b.gap
	}

	fmt.Fprintln(w)

	// Print with colors
	colPositions := make([][3]interface{}, 0) // start, end, color
	col = leftMargin
	for _, b := range arranged {
		colPositions = append(colPositions, [3]interface{}{col, col + buildingWidth, b.color})
		col += buildingWidth + b.gap
	}

	// Sky rows
	for row := 0; row < skyHeight; row++ {
		for c := 0; c < width; c++ {
			ch := grid[row][c]
			switch ch {
			case '◐':
				fmt.Fprintf(w, "%s%s%s%s", Bold, Yellow, string(ch), Reset)
			case '·', '✦', '*':
				fmt.Fprintf(w, "%s%s%s", DimWhite, string(ch), Reset)
			default:
				fmt.Fprint(w, " ")
			}
		}
		fmt.Fprintln(w)
	}

	// Building rows
	for row := skyHeight; row < len(grid); row++ {
		for c := 0; c < width; c++ {
			ch := grid[row][c]
			if ch == ' ' {
				fmt.Fprint(w, " ")
			} else if ch == '▄' {
				color := White
				for _, pos := range colPositions {
					if c >= pos[0].(int) && c < pos[1].(int) {
						color = pos[2].(string)
						break
					}
				}
				fmt.Fprintf(w, "%s%s%s", color, string(ch), Reset)
			} else if ch == '.' || (ch >= 'a' && ch <= 'z') {
				fmt.Fprintf(w, "%s%s%s", DimWhite, string(ch), Reset)
			} else if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
				fmt.Fprintf(w, "%s%s%s", BoldWhite, string(ch), Reset)
			} else {
				color := White
				for _, pos := range colPositions {
					if c >= pos[0].(int) && c < pos[1].(int) {
						color = pos[2].(string)
						break
					}
				}
				fmt.Fprintf(w, "%s%s%s", color, string(ch), Reset)
			}
		}
		fmt.Fprintln(w)
	}

	// Ground
	ground := strings.Repeat(" ", max(0, sceneLeft)) + strings.Repeat("▀", sceneWidth)
	fmt.Fprintln(w, DimWhite+ground+Reset)

	// Stats
	fmt.Fprintln(w)
	title := fmt.Sprintf("─── %s ───", projectName)
	fmt.Fprintf(w, "%s%s%s\n", BoldWhite, CenterString(title, width), Reset)

	var codeSize int64
	for _, f := range codeFiles {
		codeSize += f.Size
	}
	stats := fmt.Sprintf("%d languages · %d files · %s", len(sorted), len(codeFiles), formatSize(codeSize))
	fmt.Fprintf(w, "%s%s%s\n", Cyan, CenterString(stats, width), Reset)
	fmt.Fprintln(w)
}

// animationModel holds state for bubbletea animation
type animationModel struct {
	arranged           []building
	width              int
	leftMargin         int
	sceneLeft          int
	sceneRight         int
	sceneWidth         int
	codeFiles          []scanner.FileInfo
	projectName        string
	sorted             []extAgg
	starPositions      [][2]int
	moonCol            int
	maxBuildingHeight  int
	phase              int // 1 = rising, 2 = twinkling
	frame              int
	visibleRows        int
	shootingStarRow    int
	shootingStarCol    int
	shootingStarActive bool
	done               bool
}

type tickMsg time.Time

func tickCmd() tea.Cmd {
	return tea.Tick(60*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m animationModel) Init() tea.Cmd {
	return tickCmd()
}

func (m animationModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case tea.KeyMsg:
		// Any key exits
		m.done = true
		return m, tea.Quit
	case tickMsg:
		if m.phase == 1 {
			m.visibleRows++
			if m.visibleRows > m.maxBuildingHeight+2 {
				m.phase = 2
				m.frame = 0
			}
		} else {
			m.frame++
			// Shooting star logic
			if m.shootingStarActive {
				m.shootingStarCol += 3
				if m.shootingStarCol > m.sceneRight {
					m.shootingStarActive = false
				}
			} else if m.frame == 10 || m.frame == 28 {
				m.shootingStarActive = true
				m.shootingStarRow = rng.IntN(3)
				m.shootingStarCol = m.sceneLeft
			}
			if m.frame >= 40 {
				m.done = true
				return m, tea.Quit
			}
		}
		return m, tickCmd()
	}
	return m, nil
}

func (m animationModel) View() string {
	var sb strings.Builder

	// Draw sky
	for row := 0; row < skyHeight; row++ {
		line := make([]rune, m.width)
		for i := range line {
			line[i] = ' '
		}

		// Stars (random twinkling)
		for _, pos := range m.starPositions {
			if pos[0] == row && rng.Float32() > 0.25 {
				stars := []rune{'·', '·', '✦', '*'}
				line[pos[1]] = stars[rng.IntN(len(stars))]
			}
		}

		// Moon
		if row == 1 && m.moonCol >= 0 && m.moonCol < m.width {
			line[m.moonCol] = '◐'
		}

		// Render sky row
		for c, ch := range line {
			// Shooting star in phase 2
			if m.phase == 2 && m.shootingStarActive && row == m.shootingStarRow {
				if c >= m.shootingStarCol && c < m.shootingStarCol+3 {
					trail := []rune{'─', '─', '★'}
					sb.WriteString(Bold + Yellow + string(trail[c-m.shootingStarCol]) + Reset)
					continue
				}
			}
			switch ch {
			case '◐':
				sb.WriteString(Bold + Yellow + string(ch) + Reset)
			case '·', '✦', '*':
				sb.WriteString(DimWhite + string(ch) + Reset)
			default:
				sb.WriteString(" ")
			}
		}
		sb.WriteString("\n")
	}

	// Build column positions for coloring
	colPositions := make([][3]interface{}, 0)
	col := m.leftMargin
	for _, b := range m.arranged {
		colPositions = append(colPositions, [3]interface{}{col, col + buildingWidth, b.color})
		col += buildingWidth + b.gap
	}

	// Draw buildings
	for row := 0; row <= maxHeight; row++ {
		visibleTop := maxHeight + 1 - m.visibleRows
		if m.phase == 2 {
			visibleTop = 0 // Full buildings in phase 2
		}

		col := m.leftMargin
		line := make([]rune, m.width)
		for i := range line {
			line[i] = ' '
		}

		for _, b := range m.arranged {
			buildingTop := maxHeight - b.height
			buildingHeight := maxHeight + 1 - buildingTop - 1
			centerRow := buildingTop + 1 + buildingHeight/2

			if row >= max(buildingTop, visibleTop) && row <= maxHeight {
				if row == buildingTop && buildingTop > 0 && row >= visibleTop {
					for j := 0; j < buildingWidth; j++ {
						if col+j < m.width {
							line[col+j] = '▄'
						}
					}
				} else if row > buildingTop {
					for j := 0; j < buildingWidth; j++ {
						if col+j < m.width {
							if row == centerRow && buildingHeight >= 3 {
								extStart := (buildingWidth - len(b.extLabel)) / 2
								extEnd := extStart + len(b.extLabel)
								if j >= extStart && j < extEnd {
									line[col+j] = rune(b.extLabel[j-extStart])
								} else {
									line[col+j] = b.char
								}
							} else {
								line[col+j] = b.char
							}
						}
					}
				}
			}
			col += buildingWidth + b.gap
		}

		// Render building row
		for c, ch := range line {
			if ch == ' ' {
				sb.WriteString(" ")
			} else if ch == '▄' {
				color := White
				for _, pos := range colPositions {
					if c >= pos[0].(int) && c < pos[1].(int) {
						color = pos[2].(string)
						break
					}
				}
				sb.WriteString(color + string(ch) + Reset)
			} else if ch == '.' || (ch >= 'a' && ch <= 'z') {
				sb.WriteString(DimWhite + string(ch) + Reset)
			} else if (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') {
				sb.WriteString(BoldWhite + string(ch) + Reset)
			} else {
				color := White
				for _, pos := range colPositions {
					if c >= pos[0].(int) && c < pos[1].(int) {
						color = pos[2].(string)
						break
					}
				}
				sb.WriteString(color + string(ch) + Reset)
			}
		}
		sb.WriteString("\n")
	}

	// Ground
	ground := strings.Repeat(" ", max(0, m.sceneLeft)) + strings.Repeat("▀", m.sceneWidth)
	sb.WriteString(DimWhite + ground + Reset + "\n")

	return sb.String()
}

// renderAnimated renders animated skyline using bubbletea
func renderAnimated(w io.Writer, arranged []building, width, leftMargin, sceneLeft, sceneRight, sceneWidth int,
	codeFiles []scanner.FileInfo, projectName string, sorted []extAgg) {
	// Generate star positions
	var starPositions [][2]int
	for row := 0; row < skyHeight; row++ {
		for i := 0; i < sceneWidth/8; i++ {
			col := rng.IntN(sceneRight-sceneLeft) + sceneLeft
			if col >= 0 && col < width {
				starPositions = append(starPositions, [2]int{row, col})
			}
		}
	}

	moonCol := sceneRight - 3
	maxBuildingHeight := 0
	for _, b := range arranged {
		if b.height > maxBuildingHeight {
			maxBuildingHeight = b.height
		}
	}

	m := animationModel{
		arranged:          arranged,
		width:             width,
		leftMargin:        leftMargin,
		sceneLeft:         sceneLeft,
		sceneRight:        sceneRight,
		sceneWidth:        sceneWidth,
		codeFiles:         codeFiles,
		projectName:       projectName,
		sorted:            sorted,
		starPositions:     starPositions,
		moonCol:           moonCol,
		maxBuildingHeight: maxBuildingHeight,
		phase:             1,
		visibleRows:       1,
	}

	p := tea.NewProgram(m, tea.WithAltScreen())
	p.Run()

	// After animation, print static final frame to main screen
	renderStatic(w, arranged, width, leftMargin, sceneLeft, sceneRight, sceneWidth, codeFiles, projectName, sorted)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
