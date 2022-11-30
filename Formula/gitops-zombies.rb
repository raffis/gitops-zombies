# typed: false
# frozen_string_literal: true

# This file was generated by GoReleaser. DO NOT EDIT.
class GitopsZombies < Formula
  desc "Identify kubernetes resources which are not managed by GitOps"
  homepage "https://github.com/raffis/gitops-zombies"
  version "0.0.5"

  on_macos do
    if Hardware::CPU.arm?
      url "https://github.com/raffis/gitops-zombies/releases/download/v0.0.5/gitops-zombies_0.0.5_darwin_arm64.tar.gz"
      sha256 "e34c32ae1257759916ebc454f4a4259f01bd7ff34f0eb7f9038ebd7bd1ce40b5"

      def install
        bin.install "gitops-zombies"
      end
    end
    if Hardware::CPU.intel?
      url "https://github.com/raffis/gitops-zombies/releases/download/v0.0.5/gitops-zombies_0.0.5_darwin_amd64.tar.gz"
      sha256 "a827d11d7cd685743a75af9b4861f664bd3bc2454504353103c8d7738ac4d689"

      def install
        bin.install "gitops-zombies"
      end
    end
  end

  on_linux do
    if Hardware::CPU.arm? && Hardware::CPU.is_64_bit?
      url "https://github.com/raffis/gitops-zombies/releases/download/v0.0.5/gitops-zombies_0.0.5_linux_arm64.tar.gz"
      sha256 "9d20894b18d5b77c36d6b9321b6551949186120be2146640b26b88fbe8acfe13"

      def install
        bin.install "gitops-zombies"
      end
    end
    if Hardware::CPU.intel?
      url "https://github.com/raffis/gitops-zombies/releases/download/v0.0.5/gitops-zombies_0.0.5_linux_amd64.tar.gz"
      sha256 "c889c497369d41587cbf212da68a85b7485d961a2931d454b50a6be7da4dae24"

      def install
        bin.install "gitops-zombies"
      end
    end
  end

  test do
    system "#{bin}/gitops-zombies -h"
  end
end
