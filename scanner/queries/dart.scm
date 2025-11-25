; Dart query for functions and imports

; Function signatures (top-level and class methods)
(function_signature
  name: (identifier) @function)

; Getter signatures
(getter_signature
  (identifier) @function)

; Setter signatures
(setter_signature
  name: (identifier) @function)

; Import statements - capture the string path
(import_specification
  (configurable_uri
    (uri
      (string_literal) @import)))

; Also try library_import structure
(library_import
  (import_specification
    (configurable_uri
      (uri
        (string_literal) @import))))
