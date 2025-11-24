class Codemap < Formula
  desc "Generates a compact, visually structured 'brain map' of your codebase for LLM context"
  homepage "https://github.com/yourusername/codemap"
  url "https://github.com/yourusername/codemap/archive/v1.0.0.tar.gz"
  sha256 "REPLACE_WITH_ACTUAL_SHA256"
  license "MIT"

  depends_on "go" => :build
  depends_on "python@3.12"

  resource "rich" do
    url "https://files.pythonhosted.org/packages/source/r/rich/rich-13.7.1.tar.gz"
    sha256 "REPLACE_WITH_RICH_SHA256"
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
