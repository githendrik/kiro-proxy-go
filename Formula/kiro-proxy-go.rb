class KiroProxyGo < Formula
  desc "Kiro API Proxy - OpenAI-compatible proxy for Kiro"
  homepage "https://github.com/githendrik/kiro-proxy-go"
  url "https://github.com/githendrik/kiro-proxy-go.git"
  version "0.1.0"
  sha256 ":nochecksum"
  license "MIT"

  def install
    bin.install "kiro-proxy-go"
  end

  test do
    system "#{bin}/kiro-proxy-go", "--help"
  end
end
