#!/usr/bin/env ruby
# Binds the generated SDK trees to the package schema without requiring Pulumi's language plugins in CI.
require "digest"
require "json"

ROOT = __dir__
MANIFEST_PATH = File.join(ROOT, "sdk", "checksums.json")
SCHEMA_PATH = File.join(ROOT, "provider", "cmd", "pulumi-resource-betterstack", "schema.json")
SDK_DIRS = %w[go nodejs python dotnet java].freeze
IGNORED_DIRS = %w[node_modules bin obj __pycache__ .mypy_cache dist build .gradle].freeze

def ignored?(relative)
  parts = relative.split(File::SEPARATOR)
  parts.any? { |part| IGNORED_DIRS.include?(part) || part.end_with?(".egg-info") } || relative.end_with?(".pyc")
end

def tree_digest(root)
  digest = Digest::SHA256.new
  Dir.glob(File.join(root, "**", "*"), File::FNM_DOTMATCH).select { |path| File.file?(path) }.sort.each do |path|
    relative = path.delete_prefix("#{root}#{File::SEPARATOR}")
    next if ignored?(relative)
    digest << relative << "\0" << Digest::SHA256.file(path).hexdigest << "\n"
  end
  digest.hexdigest
end

def current_manifest
  {
    "schema_sha256" => Digest::SHA256.file(SCHEMA_PATH).hexdigest,
    "sdk_sha256" => SDK_DIRS.to_h { |language| [language, tree_digest(File.join(ROOT, "sdk", language))] }
  }
end

if ARGV == ["--write"]
  File.write(MANIFEST_PATH, "#{JSON.pretty_generate(current_manifest)}\n")
  puts "wrote #{MANIFEST_PATH}"
elsif ARGV.empty?
  abort "missing #{MANIFEST_PATH}; run `make build_sdks`" unless File.file?(MANIFEST_PATH)
  expected = JSON.parse(File.read(MANIFEST_PATH))
  current = current_manifest
  changed = []
  changed << "schema" unless expected["schema_sha256"] == current["schema_sha256"]
  SDK_DIRS.each { |language| changed << language unless expected.dig("sdk_sha256", language) == current.dig("sdk_sha256", language) }
  abort "Pulumi SDK drift in #{changed.join(', ')}; run `make build_sdks`" unless changed.empty?
  puts "Pulumi schema and SDK checksums match"
else
  abort "usage: ruby check_sdk_drift.rb [--write]"
end
