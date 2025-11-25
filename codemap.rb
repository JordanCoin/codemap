class Codemap < Formula
  desc "Generates a compact, visually structured 'brain map' of your codebase for LLM context"
  homepage "https://github.com/JordanCoin/codemap"
  url "https://github.com/JordanCoin/codemap/archive/refs/tags/v1.6.tar.gz"
  sha256 "PLACEHOLDER_SHA256"
  license "MIT"

  depends_on "go" => :build
  depends_on "python@3.12"

  include Language::Python::Virtualenv

  resource "rich" do
    url "https://files.pythonhosted.org/packages/source/r/rich/rich-13.7.1.tar.gz"
    sha256 "9be308cb1fe2f1f57d67ce99e95af38a1e2bc71ad9813b0e247cf7ffbcc3a432"
  end

  def install
    # 1. Build Go Scanner (includes deps.go for --deps mode)
    cd "scanner" do
      system "go", "build", "-o", "codemap-scanner", "."
      (libexec/"bin").install "codemap-scanner"

      # Install tree-sitter queries
      (libexec/"queries").install Dir["queries/*.scm"]

      # Build grammars for --deps mode
      (libexec/"grammars").mkpath
      system "bash", "build-grammars.sh"
      (libexec/"grammars").install Dir["grammars/*.dylib"]
      (libexec/"grammars").install Dir["grammars/*.so"]
    end

    # 2. Install Python Renderers
    (libexec/"renderer").install "renderer/render.py"
    (libexec/"renderer").install "renderer/cityscape.py"
    (libexec/"renderer").install "renderer/depgraph.py"

    # 3. Create Virtual Environment and Install Dependencies
    venv = virtualenv_create(libexec/"venv", "python3")
    venv.pip_install resources

    # 4. Install Wrapper Script
    (bin/"codemap").write <<~EOS
      #!/bin/bash
      export CODEMAP_GRAMMAR_DIR="#{libexec}/grammars"
      export CODEMAP_QUERY_DIR="#{libexec}/queries"
      "#{libexec}/bin/codemap-scanner" "$@" | "#{libexec}/venv/bin/python3" "#{libexec}/renderer/render.py"
    EOS
  end

  test do
    system "#{bin}/codemap", "."
  end
end
