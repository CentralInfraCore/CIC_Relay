(module
  (memory (export "memory") 1)
  (data (i32.const 0) "{\"data\":{\"status\":\"ok\"}}")
  (func (export "allocate") (param i32) (result i32)
    (i32.const 0))
  (func (export "deallocate") (param i32) (param i32))
  (func (export "Call") (param i64 i64) (result i64)
    ;; ptr=0, len=25. Return (25 << 32) | 0
    (i64.const 107374182400))
)
