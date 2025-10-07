# Compile test WAT files into WASM for tests. Requires `wat2wasm` from WABT.
WAT_DIR := testdata
WASM_DIR := testdata

WATS := $(wildcard $(WAT_DIR)/*.wat)
WASMS := $(patsubst $(WAT_DIR)/%.wat,$(WASM_DIR)/%.wasm,$(WATS))

.PHONY: test-wasm
test-wasm: $(WASMS)

$(WASM_DIR)/%.wasm: $(WAT_DIR)/%.wat
	@echo "  WAT2WASM  $<"
	wat2wasm $< -o $@
