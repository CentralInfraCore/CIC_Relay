(module
  (memory (export "memory") 1)
  (func (export "allocate") (param i64) (result i64)
    ;; Return a non-zero address to avoid conflict
    (i64.const 2048))
  (func (export "deallocate") (param i64) (param i64))
  (func (export "Call") (param i64) (param i64) (param i64) (param i64) (param i64) (param i64) (result i64)
    ;; Manually write the error string to memory at address 1024
    ;; String: {"error":{"code":"GUEST_ERROR","message":"oops"}}
    (i32.store (i32.const 1024) (i32.const 0x7b226572)) ;; {"er
    (i32.store (i32.const 1028) (i32.const 0x726f7222)) ;; ror"
    (i32.store (i32.const 1032) (i32.const 0x3a7b2263)) ;; :{"c
    (i32.store (i32.const 1036) (i32.const 0x6f646522)) ;; ode"
    (i32.store (i32.const 1040) (i32.const 0x3a224755)) ;; :"GU
    (i32.store (i32.const 1044) (i32.const 0x4553545f)) ;; EST_
    (i32.store (i32.const 1048) (i32.const 0x4552524f)) ;; ERRO
    (i32.store (i32.const 1052) (i32.const 0x52222c22)) ;; R","
    (i32.store (i32.const 1056) (i32.const 0x6d657373)) ;; mess
    (i32.store (i32.const 1060) (i32.const 0x61676522)) ;; age"
    (i32.store (i32.const 1064) (i32.const 0x3a226f6f)) ;; :"oo
    (i32.store (i32.const 1068) (i32.const 0x7073227d)) ;; ps"}
    (i32.store8 (i32.const 1071) (i32.const 0x7d))     ;; }

    ;; Return ptr=1024, len=47
    (i64.const 201863414784))
)
