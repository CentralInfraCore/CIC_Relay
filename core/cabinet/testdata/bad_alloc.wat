(module
  (memory (export "memory") 1)
  (func (export "allocate") (param i32) (result i32)
    (i32.const 0x00800000)) ;; way out of bounds on purpose
  (func (export "deallocate") (param i32) (param i32))
  (func (export "Call") (param i64 i64) (result i64)
    (i64.const 0))
)
