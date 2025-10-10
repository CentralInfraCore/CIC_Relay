# Compile test WAT and Go files into WASM for tests.
# Requires `wat2wasm` from WABT for .wat files.
# Requires `tinygo` for .go files.
WAT_DIR := testdata
WASM_DIR := testdata

# --- WebAssembly Text format sources ---
WATS := $(wildcard $(WAT_DIR)/*.wat)
WASMS := $(patsubst $(WAT_DIR)/%.wat,$(WASM_DIR)/%.wasm,$(WATS))

# --- Go sources for WASM ---
# Add any new Go files for WASM compilation here.
GO_SOURCES_FOR_WASM := $(wildcard $(WAT_DIR)/trap.go) $(wildcard $(WAT_DIR)/echo_json.go)
GO_WASMS := $(patsubst $(WAT_DIR)/%.go,$(WASM_DIR)/%.wasm,$(GO_SOURCES_FOR_WASM))


.PHONY: test-wasm
test-wasm: $(WASMS) $(GO_WASMS)

# Rule for .wat -> .wasm
$(WASM_DIR)/%.wasm: $(WAT_DIR)/%.wat
	@echo "  WAT2WASM  $<"
	wat2wasm $< -o $@

# Rule for .go -> .wasm
$(WASM_DIR)/%.wasm: $(WAT_DIR)/%.go
	@echo "  TINYGO    $<"
	tinygo build -o $@ -target wasi -scheduler=none $<
