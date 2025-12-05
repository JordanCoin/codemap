class Codemap < Formula
  desc "Generate a brain map of your codebase for LLM context"
  homepage "https://github.com/JordanCoin/codemap"
  url "https://github.com/JordanCoin/codemap/archive/refs/tags/v2.8.3.tar.gz"
  sha256 "63031e83aefff6c74ba4c6515a8cafc1b61b9d144300bf8192b486f4876a2f26"
  license "MIT"

  depends_on "go" => :build
  depends_on "ast-grep"

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w")

    # Install ast-grep rules for dependency analysis
    (pkgshare/"sg-rules").install Dir["scanner/sg-rules/*.yml"]
  end

  def caveats
    <<~EOS
      The --deps mode uses ast-grep for code analysis.
      Rules are installed to: #{pkgshare}/sg-rules
    EOS
  end

  test do
    # Test basic tree output
    assert_match "Files:", shell_output("#{bin}/codemap .")

    # Test help flag
    assert_match "Usage:", shell_output("#{bin}/codemap --help")
  end
end
