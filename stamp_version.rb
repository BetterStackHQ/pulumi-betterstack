#!/usr/bin/env ruby
# Stamps the provider version into schema.json. tfgen strips the version from the schema it writes
# (it re-injects at SDK-generation time); the committed artifact should carry it explicitly.
path, version = ARGV
abort("usage: stamp_version.rb <schema.json> <version>") unless path && version

s = File.read(path)
stamped = s.sub(/\A\{\n(\s*)"name"/) { "{\n#{$1}\"version\": \"#{version}\",\n#{$1}\"name\"" }
abort("stamp_version.rb: schema.json does not start with a name key; stamp failed") if stamped == s && !s.include?('"version"')
File.write(path, stamped)
