(module
  (memory (export "memory") 1)
  (func (export "Call") (param $ptr i32) (param $len i32) (result i64)
    (i64.const 0)
  )
  (func (export "allocate") (param $size i32) (result i32) (i32.const 0))
  (func (export "deallocate") (param $ptr i32) (param $size i32))
)
