; C++ query for functions and imports

; Function definitions
(function_definition
  declarator: (function_declarator
    declarator: (identifier) @function))

; Method definitions
(function_definition
  declarator: (function_declarator
    declarator: (qualified_identifier
      name: (identifier) @function)))

; #include directives
(preproc_include
  path: (string_literal) @import)

(preproc_include
  path: (system_lib_string) @import)
