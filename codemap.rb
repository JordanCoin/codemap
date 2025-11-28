class Codemap < Formula
  desc "Generate a brain map of your codebase for LLM context"
  homepage "https://github.com/JordanCoin/codemap"
  url "https://github.com/JordanCoin/codemap/archive/refs/tags/v2.0.tar.gz"
  sha256 "34035e782a3e5bd6014e1c7e697114749941f1e5b537c25aee3c1049460459b3"
  license "MIT"

  depends_on "go" => :build

  resource "tree-sitter-go" do
    url "https://github.com/tree-sitter/tree-sitter-go.git", branch: "master"
  end

  resource "tree-sitter-python" do
    url "https://github.com/tree-sitter/tree-sitter-python.git", branch: "master"
  end

  resource "tree-sitter-javascript" do
    url "https://github.com/tree-sitter/tree-sitter-javascript.git", branch: "master"
  end

  resource "tree-sitter-typescript" do
    url "https://github.com/tree-sitter/tree-sitter-typescript.git", branch: "master"
  end

  resource "tree-sitter-rust" do
    url "https://github.com/tree-sitter/tree-sitter-rust.git", branch: "master"
  end

  resource "tree-sitter-ruby" do
    url "https://github.com/tree-sitter/tree-sitter-ruby.git", branch: "master"
  end

  resource "tree-sitter-c" do
    url "https://github.com/tree-sitter/tree-sitter-c.git", branch: "master"
  end

  resource "tree-sitter-cpp" do
    url "https://github.com/tree-sitter/tree-sitter-cpp.git", branch: "master"
  end

  resource "tree-sitter-java" do
    url "https://github.com/tree-sitter/tree-sitter-java.git", branch: "master"
  end

  resource "tree-sitter-swift" do
    url "https://github.com/tree-sitter/tree-sitter-swift.git", branch: "master"
  end

  resource "tree-sitter-bash" do
    url "https://github.com/tree-sitter/tree-sitter-bash.git", branch: "master"
  end

  resource "tree-sitter-kotlin" do
    url "https://github.com/fwcd/tree-sitter-kotlin.git", branch: "main"
  end

  resource "tree-sitter-c-sharp" do
    url "https://github.com/tree-sitter/tree-sitter-c-sharp.git", branch: "master"
  end

  resource "tree-sitter-php" do
    url "https://github.com/tree-sitter/tree-sitter-php.git", branch: "master"
  end

  resource "tree-sitter-dart" do
    url "https://github.com/UserNobody14/tree-sitter-dart.git", branch: "master"
  end

  resource "tree-sitter-r" do
    url "https://github.com/r-lib/tree-sitter-r.git", branch: "main"
  end

  def install
    # Build the main Go binary
    system "go", "build", "-o", libexec/"codemap", "."

    # Create grammars directory
    (libexec/"grammars").mkpath

    # Build and install each grammar resource
    resources.each do |r|
      r.stage do
        lang = r.name.sub("tree-sitter-", "").gsub("-", "_")
        
        # Handle special source directories
        src_subdir = "src"
        src_subdir = "typescript/src" if lang == "typescript"
        src_subdir = "php/src" if lang == "php"
        
        src_dir = Pathname.pwd/src_subdir
        
        # Determine library extension and flags
        lib_ext = OS.mac? ? "dylib" : "so"
        cflags = OS.mac? ? %W[-dynamiclib -fPIC] : %W[-shared -fPIC]
        
        output_lib = libexec/"grammars/libtree-sitter-#{lang}.#{lib_ext}"
        
        # Prepare sources
        sources = [src_dir/"parser.c"]
        
        if (src_dir/"scanner.c").exist?
          sources << src_dir/"scanner.c"
        elsif (src_dir/"scanner.cc").exist?
          # Compile C++ scanner first
          system ENV.cxx, "-c", "-fPIC", src_dir/"scanner.cc", "-o", "scanner.o", "-I#{src_dir}"
          sources << "scanner.o"
        end
        
        # Compile and link
        system ENV.cc, *cflags, "-o", output_lib, *sources, "-I#{src_dir}"
      end
    end

    # Create wrapper script
    (bin/"codemap").write <<~EOS
      #!/bin/bash
      export CODEMAP_GRAMMAR_DIR="#{libexec}/grammars"
      export CODEMAP_QUERY_DIR="#{libexec}/queries"
      exec "#{libexec}/codemap" "$@"
    EOS
  end

  test do
    system bin/"codemap", "."
  end
end
