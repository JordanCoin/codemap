class Codemap < Formula
  desc "Generates a compact, visually structured 'brain map' of your codebase for LLM context"
  homepage "https://github.com/JordanCoin/codemap"
  url "https://github.com/JordanCoin/codemap/archive/refs/tags/v1.7.tar.gz"
  sha256 "3bc2ac4e2c5d1a19ce4d894841eea24ecb3b2b80c3ae383606a62293a71d43fb"
  license "MIT"

  depends_on "go" => :build

  def install
    # Build FIRST (before moving files - go:embed needs scanner/queries/ in place)
    system "go", "build", "-o", libexec/"codemap", "."

    # Build grammars for --deps mode
    (libexec/"grammars").mkpath
    cd "scanner" do
      system "bash", "build-grammars.sh"
    end
    (libexec/"grammars").install Dir["scanner/grammars/*.dylib"]
    (libexec/"grammars").install Dir["scanner/grammars/*.so"]

    # Create wrapper script with environment variables
    (bin/"codemap").write <<~EOS
      #!/bin/bash
      export CODEMAP_GRAMMAR_DIR="#{libexec}/grammars"
      export CODEMAP_QUERY_DIR="#{libexec}/queries"
      exec "#{libexec}/codemap" "$@"
    EOS
  end

  test do
    system "#{bin}/codemap", "."
  end
end
