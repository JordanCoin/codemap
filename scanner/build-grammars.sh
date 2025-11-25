#!/usr/bin/env bash
set -e

# Build tree-sitter grammar shared libraries for codemap --deps mode
# Requires: git, cc (clang/gcc)

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
GRAMMAR_DIR="$SCRIPT_DIR/grammars"
BUILD_DIR="$SCRIPT_DIR/.grammar-build"

# Detect OS for library extension
if [[ "$OSTYPE" == "darwin"* ]]; then
    LIB_EXT=".dylib"
    CC_FLAGS="-dynamiclib"
elif [[ "$OSTYPE" == "msys"* ]] || [[ "$OSTYPE" == "cygwin"* ]]; then
    LIB_EXT=".dll"
    CC_FLAGS="-shared"
else
    LIB_EXT=".so"
    CC_FLAGS="-shared -fPIC"
fi

mkdir -p "$GRAMMAR_DIR"
mkdir -p "$BUILD_DIR"

build_grammar() {
    local lang=$1
    local repo=$2
    local src_subdir=${3:-src}
    local output="$GRAMMAR_DIR/libtree-sitter-${lang}${LIB_EXT}"

    if [[ -f "$output" ]]; then
        echo "✓ $lang (already built)"
        return 0
    fi

    echo "Building $lang..."

    local clone_dir="$BUILD_DIR/tree-sitter-$lang"

    # Clone if needed
    if [[ ! -d "$clone_dir" ]]; then
        git clone --depth 1 "$repo" "$clone_dir" 2>/dev/null || {
            echo "✗ $lang (clone failed)"
            return 1
        }
    fi

    local src_dir="$clone_dir/$src_subdir"

    # Compile parser.c (and scanner.c if exists)
    local sources="$src_dir/parser.c"
    if [[ -f "$src_dir/scanner.c" ]]; then
        sources="$sources $src_dir/scanner.c"
    elif [[ -f "$src_dir/scanner.cc" ]]; then
        # C++ scanner
        c++ -c -fPIC "$src_dir/scanner.cc" -o "$BUILD_DIR/scanner_${lang}.o" -I "$src_dir" 2>/dev/null || {
            echo "✗ $lang (c++ compile failed)"
            return 1
        }
        cc $CC_FLAGS -o "$output" $sources "$BUILD_DIR/scanner_${lang}.o" -I "$src_dir" 2>/dev/null || {
            echo "✗ $lang (link failed)"
            return 1
        }
        echo "✓ $lang"
        return 0
    fi

    cc $CC_FLAGS -o "$output" $sources -I "$src_dir" 2>/dev/null || {
        echo "✗ $lang (compile failed)"
        return 1
    }
    echo "✓ $lang"
}

echo "Building tree-sitter grammars..."
echo "Output: $GRAMMAR_DIR"
echo ""

# Build each grammar (lang, repo, src_subdir)
build_grammar "go" "https://github.com/tree-sitter/tree-sitter-go" "src"
build_grammar "python" "https://github.com/tree-sitter/tree-sitter-python" "src"
build_grammar "javascript" "https://github.com/tree-sitter/tree-sitter-javascript" "src"
build_grammar "typescript" "https://github.com/tree-sitter/tree-sitter-typescript" "typescript/src"
build_grammar "rust" "https://github.com/tree-sitter/tree-sitter-rust" "src"
build_grammar "ruby" "https://github.com/tree-sitter/tree-sitter-ruby" "src"
build_grammar "c" "https://github.com/tree-sitter/tree-sitter-c" "src"
build_grammar "cpp" "https://github.com/tree-sitter/tree-sitter-cpp" "src"
build_grammar "java" "https://github.com/tree-sitter/tree-sitter-java" "src"
build_grammar "swift" "https://github.com/tree-sitter/tree-sitter-swift" "src"
build_grammar "bash" "https://github.com/tree-sitter/tree-sitter-bash" "src"
build_grammar "kotlin" "https://github.com/fwcd/tree-sitter-kotlin" "src"
build_grammar "c_sharp" "https://github.com/tree-sitter/tree-sitter-c-sharp" "src"
build_grammar "php" "https://github.com/tree-sitter/tree-sitter-php" "php/src"
build_grammar "dart" "https://github.com/UserNobody14/tree-sitter-dart" "src"
build_grammar "r" "https://github.com/r-lib/tree-sitter-r" "src"

echo ""
echo "Done. Built grammars:"
ls -1 "$GRAMMAR_DIR"/*.dylib "$GRAMMAR_DIR"/*.so "$GRAMMAR_DIR"/*.dll 2>/dev/null || echo "(none)"
