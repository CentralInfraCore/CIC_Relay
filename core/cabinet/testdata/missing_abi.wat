(module
  (memory (export "memory") 1)
  (func (export "allocate") (param i32) (result i32)
    (i32.const 0))
  ;; NOTE: deallocate is intentionally missing to trigger ABI check
  (func (export "Call") (param i64 i64) (result i64)
    (i64.const 0))
)
