class Codemap < Formula
  desc "Generate a brain map of your codebase for LLM context"
  homepage "https://github.com/JordanCoin/codemap"
  url "https://github.com/JordanCoin/codemap/archive/refs/tags/v2.5.tar.gz"
  sha256 "f83836eef6f85fda2de3be1b391a03f1f06d5d1c7daf4063e08a0672ca08d239"
  license "MIT"

  depends_on "go" => :build

  resource "tree-sitter-go" do
    url "https://github.com/tree-sitter/tree-sitter-go/archive/refs/heads/master.tar.gz"
    sha256 "1c746cac06741178f3b0b21258a8ff04599f3bef176044121b985ff17917bf25"
  end

  resource "tree-sitter-python" do
    url "https://github.com/tree-sitter/tree-sitter-python/archive/refs/heads/master.tar.gz"
    sha256 "874479d1f7058159f417fdeb706f279e4edd6b2c392bd8a642bc5d552f45e9cd"
  end

  resource "tree-sitter-javascript" do
    url "https://github.com/tree-sitter/tree-sitter-javascript/archive/refs/heads/master.tar.gz"
    sha256 "0ee41a73f53ddc31bd6567cafa5864f0678e71ebb9c9c618f6d67ca788a22455"
  end

  resource "tree-sitter-typescript" do
    url "https://github.com/tree-sitter/tree-sitter-typescript/archive/refs/heads/master.tar.gz"
    sha256 "c5bf6e925d299bce34e7ae373b46d5363f2e896e6ea282091d163fd1f831b497"
  end

  resource "tree-sitter-rust" do
    url "https://github.com/tree-sitter/tree-sitter-rust/archive/refs/heads/master.tar.gz"
    sha256 "dc93e09d9ea2b20e91c87c6202b53dc57513e0db3730231c60d154582a74dfc0"
  end

  resource "tree-sitter-ruby" do
    url "https://github.com/tree-sitter/tree-sitter-ruby/archive/refs/heads/master.tar.gz"
    sha256 "93df5566a3bee1c16f6fb7da492d58a34840d488347dc05a2327e3730e53aa20"
  end

  resource "tree-sitter-c" do
    url "https://github.com/tree-sitter/tree-sitter-c/archive/refs/heads/master.tar.gz"
    sha256 "d4e3f07154466ad60c7bb1cc9719be3bc2e8b9212d39849f6aa4a2b90cac2415"
  end

  resource "tree-sitter-cpp" do
    url "https://github.com/tree-sitter/tree-sitter-cpp/archive/refs/heads/master.tar.gz"
    sha256 "6c8f7d6bf2203b35490f5c91f81ed644a8dfb0a1e0274f15fa8e47b0b17f0d9c"
  end

  resource "tree-sitter-java" do
    url "https://github.com/tree-sitter/tree-sitter-java/archive/refs/heads/master.tar.gz"
    sha256 "a41ad5bd64ed71a026b001434319e7bcdc7e44ca1e28c39a87ab9e7ea1b1c575"
  end

  resource "tree-sitter-swift" do
    url "https://github.com/tree-sitter/tree-sitter-swift/archive/refs/heads/master.tar.gz"
    sha256 "2576f1f8da5ffa199a5b4bf1a21564106fa1f94fec19e4a375796d2286ac96bd"
  end

  resource "tree-sitter-bash" do
    url "https://github.com/tree-sitter/tree-sitter-bash/archive/refs/heads/master.tar.gz"
    sha256 "86ffaeafc9d3a01a3da828b0fab5ef1b9d765d245764b066794577daee512f23"
  end

  resource "tree-sitter-kotlin" do
    url "https://github.com/fwcd/tree-sitter-kotlin/archive/refs/heads/main.tar.gz"
    sha256 "4c63bab70f70bb884d97446a14ff78037944ff7c389f0482b5f49e4a32fe24c4"
  end

  resource "tree-sitter-c-sharp" do
    url "https://github.com/tree-sitter/tree-sitter-c-sharp/archive/refs/heads/master.tar.gz"
    sha256 "81d4d39508dad98a56969acb76e979945f1eb8c9bf3e5921a4b66bd634dc1266"
  end

  resource "tree-sitter-php" do
    url "https://github.com/tree-sitter/tree-sitter-php/archive/refs/heads/master.tar.gz"
    sha256 "df40197bb1bee56f96e52dc301ddd7e2ef0b33284dcd50de0a2bc53bd140e9a1"
  end

  resource "tree-sitter-dart" do
    url "https://github.com/UserNobody14/tree-sitter-dart/archive/refs/heads/master.tar.gz"
    sha256 "a38f682088b39813ed271eae942a3ad3c42c3c6c0086bd3484e8cdea26e04e0c"
  end

  resource "tree-sitter-r" do
    url "https://github.com/r-lib/tree-sitter-r/archive/refs/heads/main.tar.gz"
    sha256 "b6e0df92204cbd956790b20165adf13f2a9879cda5e86b70949d918302634e0f"
  end

  def install
    # Build the main Go binary
    system "go", "build", "-o", libexec/"codemap", "."

    # Create grammars directory
    (libexec/"grammars").mkpath

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
    system bin/"codemap", "."
  end
end
