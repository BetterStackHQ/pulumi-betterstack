#!/usr/bin/env ruby
# frozen_string_literal: true

# Writes the facts in the Pulumi provider's READMEs and Registry page from the committed package
# schema, the API description and go.mod, so they can't go stale: the resource and data source
# counts, the APIs the provider covers, the configuration table, the package list, the toolchain
# versions and the SDK READMEs. Prose stays hand-written; each generated part sits between
# `<!-- generated:NAME -->` and `<!-- /generated:NAME -->`. The package description and keywords in
# provider/provider.go are checked against the same API list.
#
#   ruby docs_facts.rb --write   # rewrite every generated part (make build_sdks runs it)
#   ruby docs_facts.rb --check   # fail when any generated part differs (verify.sh runs it)

require "json"

HERE = File.expand_path(__dir__)
SCHEMA = File.join(HERE, "provider/cmd/pulumi-resource-betterstack/schema.json")
SPEC = File.expand_path("../generated/spec.json", HERE)

# The bridge adds `terraformConfig` to every provider's functions; it is no data source.
BRIDGE_FUNCTIONS = %w[pulumi:providers:betterstack/terraformConfig].freeze
# Left out of the configuration table: it points the provider at another API host, for tests only.
TEST_ONLY_CONFIG = %w[endpoint].freeze
REGISTRY = "https://www.pulumi.com/registry/packages/betterstack/"
TERRAFORM_REPOSITORY = "https://github.com/BetterStackHQ/terraform-provider-betterstack"

def schema = @schema ||= JSON.parse(File.read(SCHEMA))

def repository = schema.fetch("repository")

# The Better Stack APIs the bridged Terraform provider covers, by their titles in the API description.
# A preview API's resources carry the preview build tag, which the bridge doesn't build, so Pulumi has none.
def apis
  @apis ||= JSON.parse(File.read(SPEC)).fetch("apis")
    .select { |api| api.fetch("outputs").include?("terraform") && api["distribution"] != "preview" }
    .map { |api| api.fetch("title").delete_prefix("Better Stack ").delete_suffix(" API") }
    .sort
end

def sentence_list(items) = items.size < 2 ? items.join : "#{items[0..-2].join(', ')} and #{items[-1]}"

def resource_count = schema.fetch("resources").size

def data_source_count = (schema.fetch("functions").keys - BRIDGE_FUNCTIONS).size

def go_mod = @go_mod ||= File.read(File.join(HERE, "go.mod"))

def go_module_version(path)
  go_mod[/^\s*#{Regexp.escape(path)} (v\S+)$/, 1] or abort("docs_facts.rb: go.mod has no #{path}")
end

def packages
  lang = schema.fetch("language")
  java = lang.fetch("java")
  java_package = "#{java.fetch('basePackage')}.#{java.fetch('packages').fetch(schema.fetch('name'))}"
  {
    npm: lang.fetch("nodejs").fetch("packageName"),
    pypi: lang.fetch("python").fetch("packageName"),
    # The Makefile sets the NuGet ID in the generated project: nuget.org reserves the namespace's Pulumi. prefix.
    nuget: File.read(File.join(HERE, "sdk/dotnet/Pulumi.BetterStack.csproj"))[%r{<PackageId>([^<]+)</PackageId>}, 1] || abort("sdk/dotnet/Pulumi.BetterStack.csproj names no PackageId"),
    # pulumi-java names the artifact after the package; the Makefile renames it to the Java package's last part.
    maven: "#{java.fetch('basePackage')}:#{java.fetch('packages').fetch(schema.fetch('name'))}",
    java_package: java_package,
    go: lang.fetch("go").fetch("importBasePath")
  }
end

def pypi_project = packages[:pypi].tr("_", "-")

def package_links
  p = packages
  maven_group, maven_artifact = p[:maven].split(":")
  {
    npm: "[`#{p[:npm]}`](https://www.npmjs.com/package/#{p[:npm]})",
    pypi: "[`#{p[:pypi]}`](https://pypi.org/project/#{pypi_project}/)",
    nuget: "[`#{p[:nuget]}`](https://www.nuget.org/packages/#{p[:nuget]})",
    maven: "[`#{p[:maven]}`](https://central.sonatype.com/artifact/#{maven_group}/#{maven_artifact})",
    go: "[`#{p[:go]}`](https://pkg.go.dev/#{p[:go]})"
  }
end

# --- generated parts ------------------------------------------------------------------------------

def readme_counts
  "It manages **#{resource_count} resources** and **#{data_source_count} data sources** across the " \
    "#{sentence_list(apis)} APIs through one provider. The schema also lists one more function, " \
    "`terraformConfig`, which the bridge adds to every provider."
end

def readme_toolchain
  "- Built against `pulumi-terraform-bridge/v3 #{go_module_version('github.com/pulumi/pulumi-terraform-bridge/v3')}` " \
    "(plugin-framework bridge under `v3/pkg/pf`) and `pulumi` #{go_module_version('github.com/pulumi/pulumi/pkg/v3')} " \
    "(`pkg`) and #{go_module_version('github.com/pulumi/pulumi/sdk/v3')} (`sdk`), which need Go " \
    "#{go_mod[/^go (\S+)$/, 1]} (auto-downloaded via `GOTOOLCHAIN`)."
end

def install_list
  l = package_links
  [
    "* JavaScript/TypeScript: #{l[:npm]}",
    "* Python: #{l[:pypi]}",
    "* Go: #{l[:go]}",
    "* .NET: #{l[:nuget]}",
    "* Java: #{l[:maven]} (package `#{packages[:java_package]}`)"
  ].join("\n")
end

def vendor_packages
  l = package_links
  "- Packages: npm #{l[:npm]}, PyPI #{l[:pypi]}, NuGet #{l[:nuget]}, Maven Central #{l[:maven]} " \
    "(package `#{packages[:java_package]}`), Go `#{packages[:go]}`"
end

def config_table
  vars = schema.fetch("config").fetch("variables").reject { |name, _| TEST_ONLY_CONFIG.include?(name) }
  # The token and the team first: every program sets the one and many the other.
  order = %w[apiToken teamId]
  rows = vars.sort_by { |name, _| [order.index(name) || order.size, name] }.map do |name, v|
    description = v.fetch("description").strip.gsub("|", "\\|")
    description += " Secret." if v["secret"]
    "| `betterstack:#{name}` | #{description} |"
  end
  (["| Setting | Description |", "|---|---|"] + rows).join("\n")
end

# "statusPage" -> "status pages"
def plural_words(token) = "#{token.split(":").last.gsub(/(?<!^)([A-Z])/, ' \\1').downcase}s"

def deletion_protection
  names = schema.fetch("resources").select { |_, r| r.fetch("inputProperties", {}).key?("deletionProtection") }
    .keys.map { |token| plural_words(token) }.sort
  "#{sentence_list(names).capitalize} have a `deletionProtection` input, `true` by default."
end

SDK_INSTALL = {
  "sdk/nodejs/README.md" => -> { "```bash\nnpm install #{packages[:npm]}\n```" },
  "sdk/python/pulumi_betterstack/README.md" => -> { "```bash\npip install #{pypi_project}\n```" },
  "sdk/dotnet/README.md" => -> { "```bash\ndotnet add package #{packages[:nuget]}\n```" },
  # The Go module's README, which pkg.go.dev shows on the module page.
  "sdk/go/README.md" => -> { "```bash\ngo get #{packages[:go]}\n```" },
  "sdk/java/README.md" => lambda {
    group, artifact = packages[:maven].split(":")
    "Maven Central `#{packages[:maven]}`, package `#{packages[:java_package]}`:\n\n" \
      "```xml\n<dependency>\n    <groupId>#{group}</groupId>\n    <artifactId>#{artifact}</artifactId>\n</dependency>\n```"
  }
}.freeze

def sdk_readme(path)
  <<~MD
    # Better Stack Pulumi provider

    #{schema.fetch('description')}

    #{SDK_INSTALL.fetch(path).call}

    Docs, with an example in every language: [Pulumi Registry](#{REGISTRY}). Report bugs and
    requests in [`#{repository.delete_prefix('https://github.com/')}`](#{repository}/issues).

    The provider bridges the Better Stack Terraform provider
    ([`#{TERRAFORM_REPOSITORY.delete_prefix('https://github.com/')}`](#{TERRAFORM_REPOSITORY})) and, like it,
    is licensed under #{schema.fetch('license')}.
  MD
end

# file => { part name => content }
def parts
  {
    "README.md" => { "counts" => readme_counts, "toolchain" => readme_toolchain },
    "docs/_index.md" => { "install" => install_list, "config" => config_table, "deletion-protection" => deletion_protection },
    "release/README.vendor.md" => { "packages" => vendor_packages }
  }
end

def render(path, text, generated)
  generated.reduce(text) do |out, (name, content)|
    pattern = /(<!-- generated:#{name} -->\n).*?(<!-- \/generated:#{name} -->)/m
    abort("docs_facts.rb: #{path} has no generated:#{name} part") unless out.match?(pattern)
    out.sub(pattern) { "#{Regexp.last_match(1)}#{content}\n#{Regexp.last_match(2)}" }
  end
end

# provider/provider.go feeds the package description. The keywords are search terms, not API names.
def provider_errors
  errors = []
  want = "A Pulumi package for managing errors, incidents and telemetry resources in Better Stack."
  errors << "the package description is #{schema['description'].inspect}, want #{want.inspect} (provider/provider.go)" if schema["description"] != want
  errors
end

mode = ARGV.fetch(0, nil)
abort("usage: docs_facts.rb --write|--check") unless %w[--write --check].include?(mode)

expected = parts.to_h { |path, generated| [path, render(path, File.read(File.join(HERE, path)), generated)] }
SDK_INSTALL.each_key { |path| expected[path] = sdk_readme(path) }

stale = expected.reject { |path, content| File.exist?(File.join(HERE, path)) && File.read(File.join(HERE, path)) == content }.keys
errors = provider_errors

if mode == "--write"
  stale.each { |path| File.write(File.join(HERE, path), expected.fetch(path)) }
  abort(errors.join("\n")) unless errors.empty?
else
  stale.each { |path| warn "pulumi/#{path} is stale: run `ruby pulumi/docs_facts.rb --write`" }
  errors.each { |e| warn e }
  exit(stale.empty? && errors.empty? ? 0 : 1)
end
