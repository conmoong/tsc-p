package diagnostics

// tsc-p addition. This is a new file in an upstream package (never an edit
// to an upstream file, so upstream merges cannot conflict with it): Message
// has unexported fields and can only be constructed here. Codes live in a
// 99xxxx range far above upstream's generated ranges and its 100xxx extras.

// Tscp_paris_0 carries every diagnostic produced by the @conmoong/paris
// emit plugin; the plugin composes the full message text as the argument.
var Tscp_paris_0 = &Message{code: 990101, category: CategoryError, key: "Tscp_paris_0_990101", text: "@conmoong/paris: {0}"}

// Tscp_graph_0 carries every diagnostic produced by the @conmoong/graph
// emit plugin; the plugin composes the full message text as the argument
// and sets the actual category (error/warning/message) itself via
// Diagnostic.SetCategory — this message's own default category is only
// the fallback for the rare construction path that doesn't override it.
var Tscp_graph_0 = &Message{code: 990201, category: CategoryError, key: "Tscp_graph_0_990201", text: "@conmoong/graph: {0}"}
