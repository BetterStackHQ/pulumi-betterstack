PACK         := betterstack
PROVIDER     := pulumi-resource-$(PACK)
TFGEN        := pulumi-tfgen-$(PACK)
VERSION      ?= 0.1.0
SCHEMA_DIR   := provider/cmd/$(PROVIDER)
LDFLAGS      := -X github.com/betterstackhq/pulumi-betterstack/provider.Version=$(VERSION)

# `pulumi` must be on PATH: the SDK language generators need it, and so does `schema` (tfgen shells
# out to `pulumi convert` for the HCL examples and panics without it).
export PATH := $(HOME)/.pulumi/bin:$(PATH)

# tfgen's SDK generation leaves a pulumi-package-gen-sdk* and a schema* directory in $TMPDIR for
# every language and never removes them. The SDK targets give it a directory of their own and remove
# it afterwards; one a failed run leaves behind is cleared by the next run.
GEN_TMP      := $(CURDIR)/.gen-tmp

.PHONY: tfgen schema provider test upgrade_test build_go_sdk build_sdks clean

# Regenerate the Pulumi Package Schema + bridge metadata from the bridged Terraform provider
# (../generated/terraform), then stamp the version (tfgen strips it; the committed schema
# should carry it).
schema:
	(cd provider && go run -ldflags "$(LDFLAGS)" ./cmd/$(TFGEN) schema --out cmd/$(PROVIDER))
	ruby stamp_version.rb $(SCHEMA_DIR)/schema.json $(VERSION)
	ruby java_examples.rb --write

# Build the provider plugin binary (embeds schema.json).
provider: schema
	(cd provider && go build -ldflags "$(LDFLAGS)" -o ../bin/$(PROVIDER) ./cmd/$(PROVIDER))

# Native tests: schema-coverage (every TF resource/data source bridges) + lifecycle CRUD via a mock API.
# Coverage only where the tests live: -cover across the test-less cmd/ mains needs the covdata
# tool, which auto-downloaded GOTOOLCHAINs may lack.
test:
	(cd provider && go test . -cover && go test ./cmd/...)

# Upgrade test: applies every resource with the plugin built from BASELINE (a git ref, default
# origin/main; CI uses the last vX.Y.Z release tag), then previews it with this tree's build and
# fails on any replacement or deletion.
BASELINE ?= origin/main
upgrade_test:
	rm -rf .baseline && git worktree add --detach .baseline $(BASELINE)
	(cd .baseline/pulumi/provider && go build -o ../../../bin/baseline/$(PROVIDER) ./cmd/$(PROVIDER)); \
	  status=$$?; git worktree remove --force .baseline; [ $$status -eq 0 ]
	# Verbose into a log so the summary line shows; the whole log only when the test fails.
	cd provider && PULUMI_BASELINE_PROVIDER=$(CURDIR)/bin/baseline/$(PROVIDER) \
	  go test . -run TestUpgrade -count=1 -v > ../bin/upgrade_test.log 2>&1; \
	  status=$$?; grep -E '^Upgrade test:' ../bin/upgrade_test.log; \
	  if [ $$status -ne 0 ]; then cat ../bin/upgrade_test.log; fi; exit $$status

# Language generation must run from this directory: tfgen reads $(SCHEMA_DIR)/schema.json
# relative to its cwd when emitting SDKs. sdk/go/go.mod is hand-seeded (tfgen does not emit it),
# so only the generated package dir is cleared.
# The Go SDK requires the Pulumi SDK the provider is built with: older ones fail at runtime on
# data-source calls that take an Output (v3.246.0: "cannot marshal Output value").
PULUMI_SDK_VERSION = $(shell go list -m -f '{{.Version}}' github.com/pulumi/pulumi/sdk/v3)

build_go_sdk: schema
	rm -rf sdk/go/betterstack $(GEN_TMP) && mkdir -p $(GEN_TMP)
	TMPDIR=$(GEN_TMP) go run -ldflags "$(LDFLAGS)" ./provider/cmd/$(TFGEN) go --out sdk/go
	rm -rf $(GEN_TMP)
	(cd sdk/go && go get github.com/pulumi/pulumi/sdk/v3@$(PULUMI_SDK_VERSION) && go mod tidy && go build ./...)

# All SDKs (publish prep). The pulumi-language-* plugins (~/.pulumi/bin) do the emitting;
# compiling/publishing the emitted python/nodejs/dotnet packages needs those toolchains.
# Java SDK generation runs through pulumi-java's language plugin, pinned here for every caller.
PULUMI_JAVA_VERSION := v1.40.2
install_java_plugin:
	pulumi plugin install language java $(PULUMI_JAVA_VERSION)

build_sdks: schema
	rm -rf sdk/go/betterstack sdk/python sdk/nodejs sdk/dotnet sdk/java $(GEN_TMP) && mkdir -p $(GEN_TMP)
	TMPDIR=$(GEN_TMP) go run -ldflags "$(LDFLAGS)" ./provider/cmd/$(TFGEN) go     --out sdk/go
	TMPDIR=$(GEN_TMP) go run -ldflags "$(LDFLAGS)" ./provider/cmd/$(TFGEN) python --out sdk/python
	TMPDIR=$(GEN_TMP) go run -ldflags "$(LDFLAGS)" ./provider/cmd/$(TFGEN) nodejs --out sdk/nodejs
	TMPDIR=$(GEN_TMP) go run -ldflags "$(LDFLAGS)" ./provider/cmd/$(TFGEN) dotnet --out sdk/dotnet
	# The generator downloads logoUrl into the NuGet icon, which must be PNG or JPEG, and logoUrl is the
	# SVG the Pulumi Registry shows: the icon is the committed PNG instead.
	cp assets/logo.png sdk/dotnet/logo.png
	# The NuGet package ID would be the project name, Pulumi.BetterStack, but nuget.org reserves the Pulumi.
	# prefix for Pulumi. The namespace stays Pulumi.BetterStack. Fails if the generated line ever changes.
	grep -qx '    <GeneratePackageOnBuild>true</GeneratePackageOnBuild>' sdk/dotnet/Pulumi.BetterStack.csproj
	sed -i.bak 's#^    <GeneratePackageOnBuild>true</GeneratePackageOnBuild>$$#&\n    <PackageId>BetterStack.Pulumi</PackageId>#' sdk/dotnet/Pulumi.BetterStack.csproj && rm sdk/dotnet/Pulumi.BetterStack.csproj.bak
	# The generated project packs no README, so nuget.org shows "Your package is missing a README".
	grep -qx '</Project>' sdk/dotnet/Pulumi.BetterStack.csproj
	sed -i.bak -e 's#^    <PackageId>BetterStack.Pulumi</PackageId>$$#&\n    <PackageReadmeFile>README.md</PackageReadmeFile>#' \
	  -e 's#^</Project>$$#  <ItemGroup>\n    <None Include="README.md" Pack="True" PackagePath="" />\n  </ItemGroup>\n\n&#' sdk/dotnet/Pulumi.BetterStack.csproj && rm sdk/dotnet/Pulumi.BetterStack.csproj.bak
	# NuGet's search tags are the schema's keywords, as npm and PyPI get them (less the Registry's category tag).
	ruby -rjson -e 'f = ARGV[1]; tags = JSON.parse(File.read(ARGV[0]))["keywords"].reject { |k| k.start_with?("category/") }.join(";"); \
	  s = File.read(f).sub(%r{^    <PackageReadmeFile>README.md</PackageReadmeFile>\n}) { |m| "#{m}    <PackageTags>#{tags}</PackageTags>\n" }; \
	  abort("no PackageReadmeFile line in #{f}") unless s.include?("<PackageTags>"); File.write(f, s)' $(SCHEMA_DIR)/schema.json sdk/dotnet/Pulumi.BetterStack.csproj
	TMPDIR=$(GEN_TMP) go run -ldflags "$(LDFLAGS)" ./provider/cmd/$(TFGEN) java   --out sdk/java
	# pulumi-java names the Maven artifact after the Pulumi package (betterstack) and has no setting for it;
	# the published coordinates are com.betterstack:pulumi. Fails if the generated line ever changes.
	grep -qx '            artifactId = "betterstack"' sdk/java/build.gradle
	sed -i.bak 's/^            artifactId = "betterstack"$$/            artifactId = "pulumi"/' sdk/java/build.gradle && rm sdk/java/build.gradle.bak
	ruby java_examples.rb --write
	rm -rf $(GEN_TMP)
	printf '%s\n' '$(VERSION)' > sdk/dotnet/version.txt
	(cd sdk/go && go get github.com/pulumi/pulumi/sdk/v3@$(PULUMI_SDK_VERSION) && go mod tidy && go build ./...)
	ruby docs_facts.rb --write
	ruby check_sdk_drift.rb --write

clean:
	rm -rf bin $(GEN_TMP)
