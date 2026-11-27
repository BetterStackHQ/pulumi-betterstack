#!/usr/bin/env ruby
# frozen_string_literal: true

# Points the Java examples' imports at the Java SDK's real package. tfgen converts every example
# with pulumi-java's program generator, which ignores `JavaInfo.Packages` (and, for input types and
# functions, `BasePackage` too): it imports `com.betterstack.betterstack.Monitor` and
# `com.pulumi.betterstack.inputs.GetSourceArgs`, while the SDK is `com.betterstack.pulumi`
# (provider/provider.go). No bridge setting reaches that generator and no hook runs between the
# conversion and the output, so the generated schema.json and the Java SDK's javadoc are rewritten
# here, from the schema's own `language.java`.
#
#   ruby java_examples.rb --write   # make schema and make build_sdks run it
#   ruby java_examples.rb --check   # every Java import in schema.json resolves to the SDK (verify.sh)

require "json"

HERE = File.expand_path(__dir__)
SCHEMA = File.join(HERE, "provider/cmd/pulumi-resource-betterstack/schema.json")
JAVA_SRC = File.join(HERE, "sdk/java/src/main/java")
# Classes the examples take from Pulumi's own Java SDK (com.pulumi:pulumi), not from this package.
PULUMI_SDK = /\Acom\.pulumi\.(Context|Pulumi|core\.|resources\.|deployment\.|asset\.|codegen\.)/
IMPORT = /import (?:static )?(com\.[A-Za-z0-9_.]+?)(?:\.\*)?;/

schema = JSON.parse(File.read(SCHEMA))
name = schema.fetch("name")
java = schema.fetch("language").fetch("java")
target = "#{java.fetch('basePackage')}.#{java.fetch('packages').fetch(name)}."
wrong = ["#{java.fetch('basePackage')}.#{name}.", "com.pulumi.#{name}."].uniq - [target]

def java_files = Dir[File.join(JAVA_SRC, "**/*.java")]

def rewrite(path, wrong, target)
  text = File.read(path)
  fixed = wrong.reduce(text) { |out, prefix| out.gsub("import #{prefix}", "import #{target}").gsub("import static #{prefix}", "import static #{target}") }
  File.write(path, fixed) if fixed != text
end

case ARGV.fetch(0, nil)
when "--write"
  rewrite(SCHEMA, wrong, target)
  java_files.each { |path| rewrite(path, wrong, target) } if Dir.exist?(JAVA_SRC)
when "--check"
  errors = []
  imports = File.read(SCHEMA).scan(IMPORT).flatten.uniq
  abort("java_examples.rb: schema.json has no Java imports; the check would prove nothing") if imports.empty?
  imports.each do |imp|
    next if imp.match?(PULUMI_SDK)
    # Another provider's Java SDK, such as com.pulumi.aws for an example that sets up a CloudFormation
    # stack: it is its own package, and only this provider's classes must be in the SDK.
    next if imp.start_with?("com.pulumi.") && !imp.start_with?("com.pulumi.#{name}.")
    unless imp.start_with?(target)
      errors << "schema.json imports #{imp}, outside #{target.chomp('.')}"
      next
    end
    # A static import names a member of a class; the class is the file.
    candidates = [imp, imp.sub(/\.[^.]+\z/, "")].map { |c| File.join(JAVA_SRC, "#{c.tr('.', '/')}.java") }
    errors << "schema.json imports #{imp}, which the Java SDK lacks" if candidates.none? { |f| File.exist?(f) }
  end
  stale = java_files.select { |f| wrong.any? { |prefix| File.read(f).include?("import #{prefix}") } }
  errors << "#{stale.size} Java SDK files import #{wrong.join(' or ')}: run `make build_sdks`" unless stale.empty?
  errors.each { |e| warn e }
  exit(errors.empty? ? 0 : 1)
else
  abort("usage: java_examples.rb --write|--check")
end
