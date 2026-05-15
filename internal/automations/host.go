package automations

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/tetratelabs/wazero/api"
)

// Host module name — plugins import functions from this namespace.
const hostModuleName = "beads"

// registerHostFunctions adds the beads host module with functions that
// WASM plugins can call. Registered once per runtime; uses the module
// registry to look up per-module state at call time.
//
//   - bd_get_issue_len() -> i32: returns length of current issue JSON
//   - bd_get_issue(ptr i32, len i32) -> i32: copies issue JSON to guest memory
//   - bd_kv_get(key_ptr i32, key_len i32, val_ptr i32, val_cap i32) -> i32: get KV value
//   - bd_kv_set(key_ptr i32, key_len i32, val_ptr i32, val_len i32) -> i32: set KV value
//   - bd_log(level_ptr i32, level_len i32, msg_ptr i32, msg_len i32): structured log
//   - bd_emit_event(ptr i32, len i32): emit a plugin event
func (r *Runtime) registerHostFunctions(ctx context.Context) error {
	// Helper to find the Module for the calling WASM module.
	findModule := func(mod api.Module) *Module {
		r.mu.Lock()
		defer r.mu.Unlock()
		return r.moduleState[mod.Name()]
	}

	_, err := r.engine.NewHostModuleBuilder(hostModuleName).
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, caller api.Module) uint32 {
			if m := findModule(caller); m != nil {
				return uint32(len(m.inputBuf))
			}
			return 0
		}).
		WithName("bd_get_issue_len").
		Export("bd_get_issue_len").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, caller api.Module, ptr, bufLen uint32) uint32 {
			m := findModule(caller)
			if m == nil {
				return 0
			}
			data := m.inputBuf
			if uint32(len(data)) > bufLen {
				data = data[:bufLen]
			}
			if !caller.Memory().Write(ptr, data) {
				return 0
			}
			return uint32(len(data))
		}).
		WithName("bd_get_issue").
		Export("bd_get_issue").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, caller api.Module, keyPtr, keyLen, valPtr, valCap uint32) uint32 {
			m := findModule(caller)
			if m == nil {
				return 0
			}
			keyBytes, ok := caller.Memory().Read(keyPtr, keyLen)
			if !ok {
				return 0
			}
			namespacedKey := m.name + ":" + string(keyBytes)
			val, err := r.KV.Get(namespacedKey)
			if err != nil || val == nil {
				return 0
			}
			if uint32(len(val)) > valCap {
				val = val[:valCap]
			}
			if !caller.Memory().Write(valPtr, val) {
				return 0
			}
			return uint32(len(val))
		}).
		WithName("bd_kv_get").
		Export("bd_kv_get").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, caller api.Module, keyPtr, keyLen, valPtr, valLen uint32) uint32 {
			m := findModule(caller)
			if m == nil {
				return 0
			}
			keyBytes, ok := caller.Memory().Read(keyPtr, keyLen)
			if !ok {
				return 0
			}
			valBytes, ok := caller.Memory().Read(valPtr, valLen)
			if !ok {
				return 0
			}
			namespacedKey := m.name + ":" + string(keyBytes)
			if err := r.KV.Set(namespacedKey, valBytes); err != nil {
				return 0
			}
			return 1
		}).
		WithName("bd_kv_set").
		Export("bd_kv_set").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, caller api.Module, levelPtr, levelLen, msgPtr, msgLen uint32) {
			m := findModule(caller)
			if m == nil {
				return
			}
			level, ok := caller.Memory().Read(levelPtr, levelLen)
			if !ok {
				return
			}
			msg, ok := caller.Memory().Read(msgPtr, msgLen)
			if !ok {
				return
			}
			if r.Logger != nil {
				r.Logger.Log(m.name, string(level), string(msg))
			}
		}).
		WithName("bd_log").
		Export("bd_log").
		NewFunctionBuilder().
		WithFunc(func(ctx context.Context, caller api.Module, ptr, length uint32) {
			m := findModule(caller)
			if m == nil {
				return
			}
			data, ok := caller.Memory().Read(ptr, length)
			if !ok {
				return
			}
			var event map[string]interface{}
			if err := json.Unmarshal(data, &event); err != nil {
				return
			}
			if r.Logger != nil {
				r.Logger.Log(m.name, "event", fmt.Sprintf("%v", event))
			}
		}).
		WithName("bd_emit_event").
		Export("bd_emit_event").
		Instantiate(ctx)

	return err
}
