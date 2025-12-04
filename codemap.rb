class Codemap < Formula
  desc "Generate a brain map of your codebase for LLM context"
  homepage "https://github.com/JordanCoin/codemap"
  url "https://github.com/JordanCoin/codemap/archive/refs/tags/v2.8.3.tar.gz"
  sha256 "63031e83aefff6c74ba4c6515a8cafc1b61b9d144300bf8192b486f4876a2f26"
  license "MIT"

  depends_on "go" => :build

  resource "tree-sitter-go" do
    url "https://github.com/tree-sitter/tree-sitter-go/archive/refs/tags/v0.25.0.tar.gz"
    sha256 "2dc241b97872c53195e01b86542b411a3c1a6201d9c946c78d5c60c063bba1ef"
  end

  resource "tree-sitter-python" do
    url "https://github.com/tree-sitter/tree-sitter-python/archive/refs/tags/v0.25.0.tar.gz"
    sha256 "4609a3665a620e117acf795ff01b9e965880f81745f287a16336f4ca86cf270c"
  end

  resource "tree-sitter-javascript" do
    url "https://github.com/tree-sitter/tree-sitter-javascript/archive/refs/tags/v0.25.0.tar.gz"
    sha256 "9712fc283d3dc01d996d20b6392143445d05867a7aad76fdd723824468428b86"
  end

  resource "tree-sitter-typescript" do
    url "https://github.com/tree-sitter/tree-sitter-typescript/archive/refs/tags/v0.23.2.tar.gz"
    sha256 "2c4ce711ae8d1218a3b2f899189298159d672870b5b34dff5d937bed2f3e8983"
  end

  resource "tree-sitter-rust" do
    url "https://github.com/tree-sitter/tree-sitter-rust/archive/refs/tags/v0.24.0.tar.gz"
    sha256 "79c9eb05af4ebcce8c40760fc65405e0255e2d562702314b813a5dec1273b9a2"
  end

  resource "tree-sitter-ruby" do
    url "https://github.com/tree-sitter/tree-sitter-ruby/archive/refs/tags/v0.23.1.tar.gz"
    sha256 "e7e49577ddc1f2de8e42d42353b477e338c15bbb95b2558e123ddc13d88789f0"
  end

  resource "tree-sitter-c" do
    url "https://github.com/tree-sitter/tree-sitter-c/archive/refs/tags/v0.24.1.tar.gz"
    sha256 "25dd4bb3dec770769a407e0fc803f424ce02c494a56ce95fedc525316dcf9b48"
  end

  resource "tree-sitter-cpp" do
    url "https://github.com/tree-sitter/tree-sitter-cpp/archive/refs/tags/v0.23.4.tar.gz"
    sha256 "7a2c55afe3028f4105f25762ea58cc16537d1f5a1dcd9cca90410b3cd5d46051"
  end

  resource "tree-sitter-java" do
    url "https://github.com/tree-sitter/tree-sitter-java/archive/refs/tags/v0.23.5.tar.gz"
    sha256 "cb199e0faae4b2c08425f88cbb51c1a9319612e7b96315a174a624db9bf3d9f0"
  end

  resource "tree-sitter-bash" do
    url "https://github.com/tree-sitter/tree-sitter-bash/archive/refs/tags/v0.25.1.tar.gz"
    sha256 "2e785a761225b6c433410ef9c7b63cfb0a4e83a35a19e0f2aec140b42c06b52d"
  end

  resource "tree-sitter-kotlin" do
    url "https://github.com/fwcd/tree-sitter-kotlin/archive/refs/tags/0.3.8.tar.gz"
    sha256 "7dd60975786bf9cb4be6a5176f5ccb5fed505f9929a012da30762505b1015669"
  end

  resource "tree-sitter-c-sharp" do
    url "https://github.com/tree-sitter/tree-sitter-c-sharp/archive/refs/tags/v0.23.1.tar.gz"
    sha256 "c0b008dca3c6820604bf0853b9668ba034f9750d89d170ba834261e94e2cd917"
  end

  resource "tree-sitter-php" do
    url "https://github.com/tree-sitter/tree-sitter-php/archive/refs/tags/v0.24.2.tar.gz"
    sha256 "0e73ad63dda67ac12c0e012726a4e1a9811c26b020a0a2dea3e889f8246d9cf4"
  end

  resource "tree-sitter-r" do
    url "https://github.com/r-lib/tree-sitter-r/archive/refs/tags/v1.2.0.tar.gz"
    sha256 "a95b6e79a40b6b906a3b61ec040c15422c549d599e01d87092bf7ae78ebcadc5"
  end

  def install
    # Build the main Go binary
    system "go", "build", "-o", libexec/"codemap", "."

    # Create grammars directory
    (libexec/"grammars").mkpath

    # Copy query files
    (libexec/"queries").mkpath
    cp_r Dir["scanner/queries/*.scm"], libexec/"queries/"

    # Build and install each grammar resource
    resources.each do |r|
      r.stage do
        lang = r.name.sub("tree-sitter-", "").tr("-", "_")

        # Handle special source directories
        src_subdir = "src"
        src_subdir = "typescript/src" if lang == "typescript"
        src_subdir = "php/src" if lang == "php"

        src_dir = Pathname.pwd/src_subdir

        # Determine library extension and flags
        lib_ext = OS.mac? ? "dylib" : "so"
        cflags = OS.mac? ? %w[-dynamiclib -fPIC] : %w[-shared -fPIC]

        output_lib = libexec/"grammars/libtree-sitter-#{lang}.#{lib_ext}"

        # Prepare sources
        sources = [src_dir/"parser.c"]

        if (src_dir/"scanner.c").exist?
          sources << (src_dir/"scanner.c")
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
    # Test basic tree output
    assert_match "Files:", shell_output("#{bin}/codemap .")

    # Test help flag
    assert_match "Usage:", shell_output("#{bin}/codemap --help")
  end
end
