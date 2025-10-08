(module
  (memory (export "memory") 1)
  (func (export "allocate") (param i64) (result i64)
    (i64.const 0))
  (func (export "deallocate") (param i64) (param i64))
  (func (export "Call") (param $op_ptr i64) (param $op_len i64) (param $auth_ptr i64) (param $auth_len i64) (param $data_ptr i64) (param $data_len i64) (result i64)
    ;; This function runs a long loop to simulate a timeout.
    (local i32) ;; This is local at index 6 (after the six i64 params)
    (loop
      (local.get 6) ;; get the loop counter
      (i32.const 1)
      (i32.add)
      (local.set 6) ;; update the loop counter
      (local.get 6)
      (i32.const 2000000000) ;; A large number to ensure timeout
      (i32.lt_u)
      (br_if 0) ;; continue loop if counter is less
    )
    ;; Return 0,0 like the dummy module.
    (i64.const 0)
  )
)
