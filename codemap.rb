class Codemap < Formula
  desc "Generates a compact, visually structured 'brain map' of your codebase for LLM context"
  homepage "https://github.com/JordanCoin/codemap"
  url "https://github.com/JordanCoin/codemap/archive/refs/tags/v1.4.tar.gz"
  sha256 "3e8b5cc67e79d8a7583bf10bca0dfc384504a8dbee29029d184252a312719584"
  license "MIT"

  depends_on "go" => :build
  depends_on "python@3.12"

  include Language::Python::Virtualenv

  resource "rich" do
    url "https://files.pythonhosted.org/packages/source/r/rich/rich-13.7.1.tar.gz"
    sha256 "9be308cb1fe2f1f57d67ce99e95af38a1e2bc71ad9813b0e247cf7ffbcc3a432"
  end

  # Add other python dependencies if needed (e.g. markdown-it-py, pygments, etc.)
  # For simplicity in this template, we assume rich is the main one. 
  # In a real formula, you'd use `poet` to generate all resource blocks.

  def install
    # 1. Build Go Scanner
    cd "scanner" do
      system "go", "build", "-o", "codemap-scanner", "main.go"
      (libexec/"bin").install "codemap-scanner"
    end

    # 2. Install Python Renderer
    (libexec/"renderer").install "renderer/render.py"
    (libexec/"renderer").install "renderer/cityscape.py"

    # 3. Create Virtual Environment and Install Dependencies
    venv = virtualenv_create(libexec/"venv", "python3")
    venv.pip_install resources

    # 4. Install Wrapper Script
    # We create a wrapper that points to the artifacts in libexec
    (bin/"codemap").write <<~EOS
      #!/bin/bash
      "#{libexec}/bin/codemap-scanner" "$@" | "#{libexec}/venv/bin/python3" "#{libexec}/renderer/render.py"
    EOS
  end

  test do
    # Simple test to verify it runs
    system "#{bin}/codemap", "."
  end
end
