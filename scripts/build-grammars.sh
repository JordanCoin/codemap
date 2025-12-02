#!/bin/bash
# build-grammars.sh - Build tree-sitter grammars for the current platform
#
# Usage: ./scripts/build-grammars.sh [target]
#   target: darwin, linux, or windows (defaults to current OS)
#
# Environment variables:
#   CC  - C compiler (auto-detected if not set)
#   CXX - C++ compiler (auto-detected if not set)

set -e

# Grammar definitions: lang:repo:branch:src_dir
GRAMMARS=(
  "go:tree-sitter/tree-sitter-go:master:src"
  "python:tree-sitter/tree-sitter-python:master:src"
  "javascript:tree-sitter/tree-sitter-javascript:master:src"
  "typescript:tree-sitter/tree-sitter-typescript:master:typescript/src"
  "rust:tree-sitter/tree-sitter-rust:master:src"
  "ruby:tree-sitter/tree-sitter-ruby:master:src"
  "c:tree-sitter/tree-sitter-c:master:src"
  "cpp:tree-sitter/tree-sitter-cpp:master:src"
  "java:tree-sitter/tree-sitter-java:master:src"
  "swift:tree-sitter/tree-sitter-swift:master:src"
  "bash:tree-sitter/tree-sitter-bash:master:src"
  "kotlin:fwcd/tree-sitter-kotlin:main:src"
  "c_sharp:tree-sitter/tree-sitter-c-sharp:master:src"
  "php:tree-sitter/tree-sitter-php:master:php/src"
  "dart:UserNobody14/tree-sitter-dart:master:src"
  "r:r-lib/tree-sitter-r:main:src"
)

# Detect target platform
TARGET="${1:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
case "$TARGET" in
  darwin|macos)
    TARGET="darwin"
    EXT="dylib"
    CC="${CC:-clang}"
    CXX="${CXX:-clang++}"
    SHARED_FLAGS="-shared -fPIC"
    ;;
  linux)
    EXT="so"
    CC="${CC:-gcc}"
    CXX="${CXX:-g++}"
    SHARED_FLAGS="-shared -fPIC"
    ;;
  windows|win)
    TARGET="windows"
    EXT="dll"
    # Default to Zig for Windows cross-compilation
    CC="${CC:-zig cc -target x86_64-windows-gnu}"
    CXX="${CXX:-zig c++ -target x86_64-windows-gnu}"
    SHARED_FLAGS="-shared -fPIC"
    ;;
  *)
    echo "Unknown target: $TARGET"
    echo "Usage: $0 [darwin|linux|windows]"
    exit 1
    ;;
esac

echo "Building grammars for $TARGET (.$EXT)"
echo "CC=$CC"
echo "CXX=$CXX"
echo ""

mkdir -p grammars
BUILD_DIR=$(mktemp -d)
trap "rm -rf $BUILD_DIR" EXIT

for grammar in "${GRAMMARS[@]}"; do
  IFS=: read -r lang repo branch src_dir <<< "$grammar"

  echo "Building $lang..."

  # Download and extract to clean temp dir
  rm -rf "$BUILD_DIR"/*
  cd "$BUILD_DIR"
  curl -sL "https://github.com/$repo/archive/refs/heads/$branch.tar.gz" | tar xz
  extracted_dir=$(ls -d */ | head -1)
  src_path="${extracted_dir}${src_dir}"

  if [ ! -d "$src_path" ]; then
    echo "  ✗ Source not found: $src_path"
    exit 1
  fi

  # Compile
  sources="$src_path/parser.c"
  use_cxx=false

  if [ -f "$src_path/scanner.c" ]; then
    sources="$sources $src_path/scanner.c"
  elif [ -f "$src_path/scanner.cc" ]; then
    use_cxx=true
    $CXX -c -fPIC -I"$src_path" "$src_path/scanner.cc" -o scanner.o
    sources="$sources scanner.o"
  fi

  output_file="$OLDPWD/grammars/libtree-sitter-$lang.$EXT"

  if $use_cxx; then
    $CXX $SHARED_FLAGS -I"$src_path" $sources -o "$output_file"
  else
    $CC $SHARED_FLAGS -I"$src_path" $sources -o "$output_file"
  fi

  cd "$OLDPWD"
  echo "  ✓ Built $lang"
done

echo ""
echo "Built ${#GRAMMARS[@]} grammars:"
ls -la grammars/
