package main

// Register compile-time (trusted, performance-critical) pre_request/
// post_response hooks here — the one blessed touch-point for forks, per
// docs/design/09-extensibility.md "Delivery mechanisms" (a). Nothing is
// registered by default; a fork adds an import + one call and rebuilds:
//
//	func init() {
//		extension.Register(mypkg.MyHook{}, extension.PreRequest)
//	}
//
// Webhook- and WASM-backed hooks don't belong here — those are DB rows
// (ai_extensions, managed via the admin API) loaded by
// biz.NewExtensionDispatcher, no rebuild required.
func init() {
}
