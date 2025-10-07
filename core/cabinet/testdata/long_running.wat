(module
  (memory (export "memory") 1)
  (func (export "allocate") (param i32) (result i32)
    (i32.const 0))
  (func (export "deallocate") (param i32) (param i32))
  (func (export "Call") (param i64 i64) (result i64)
    ;; This function runs a long loop to simulate a timeout.
    (local i32) ;; This is local at index 2 (after the two i64 params)
    (loop
      (local.get 2) ;; get the loop counter
      (i32.const 1)
      (i32.add)
      (local.set 2) ;; update the loop counter
      (local.get 2)
      (i32.const 2000000000) ;; A large number to ensure timeout
      (i32.lt_u)
      (br_if 0) ;; continue loop if counter is less
    )
    ;; Return 0,0 like the dummy module.
    (i64.const 0)
  )
)
