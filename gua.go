package gua

import (
	"fmt"
	"log"
	"reflect"
	"strings"
	"sync"

	lua "github.com/yuin/gopher-lua"
)

var _luax *Luax

var once sync.Once

type Luax struct {
	L *lua.LState
}

type ServiceFuncs struct {
	N string                    // name of service
	V reflect.Value             // receiver of methods for the service
	M map[string]reflect.Method // registered methods
}

func NewState(opts ...Option) *Luax {

	once.Do(func() {
		opt := lua.Options{}
		for _, o := range opts {
			o(&opt)
		}
		_luax = &Luax{L: lua.NewState(opt)}
	})
	return _luax
}

func (l *Luax) Close() {
	l.L.Close()
}

func (l *Luax) SetGlobal(v ...any) {
	for _, v := range v {
		ms := register_global(v)
		for _, m := range ms.M {
			var i = getIns()
			makeSum(&i, m, ms.V)
			l.L.SetGlobal(m.Name, l.L.NewFunction(i))
		}
	}
}
func (l *Luax) Module(v ...any) {
	for _, v := range v {
		ms := register_global(v)
		lgfuncs := method_lgfunc(ms)
		mod := l.L.SetFuncs(l.L.NewTable(), lgfuncs)
		i := getIns()
		make_mod(&i, mod)
		pkPath := strings.Split(ms.N, "/")
		pkName := pkPath[len(pkPath)-1]
		names := strings.Split(pkName, ".")
		names[len(names)-1] = strings.ToLower(names[len(names)-1])
		pkPath = pkPath[:len(pkPath)-1]
		pkPath = append(pkPath, names...)
		mname := strings.Join(pkPath, "/")
		log.Printf("preload module: [%s]", mname)
		l.L.PreloadModule(mname, i)
	}
}

func make_mod(fptr any, mod *lua.LTable) {
	// 检查fptr是否是指针类型
	fn := reflect.ValueOf(fptr)
	k := fn.Kind()
	if k == reflect.Pointer {
		fn = fn.Elem()
	}
	res := reflect.MakeFunc(fn.Type(), func(args []reflect.Value) []reflect.Value {
		L := args[0].Interface().(*lua.LState)
		L.Push(mod)
		return []reflect.Value{reflect.ValueOf(1)}
	})
	fn.Set(res)
}

func method_lgfunc(ms *ServiceFuncs) map[string]lua.LGFunction {
	lgfuncs := map[string]lua.LGFunction{}
	for _, m := range ms.M {
		var i = getIns()
		makeSum(&i, m, ms.V)
		lgfuncs[m.Name] = i
	}
	return lgfuncs
}

// DoString executes the given Lua code string.
func (l *Luax) DoString(code string) error {
	return l.L.DoString(code)
}

// DoFile executes the given Lua code file.
func (l *Luax) DoFile(filename string) error {
	return l.L.DoFile(filename)
}

func makeSum(fptr any, m reflect.Method, v reflect.Value) {
	// 检查fptr是否是指针类型
	fn := reflect.ValueOf(fptr)
	k := fn.Kind()
	if k == reflect.Pointer {
		fn = fn.Elem()
	}
	res := reflect.MakeFunc(fn.Type(), func(args []reflect.Value) []reflect.Value {
		L := args[0].Interface().(*lua.LState)
		params := []reflect.Value{v}
		for i := 1; i < m.Type.NumIn(); i++ {
			n := m.Type.In(i)
			isPtr := n.Kind() == reflect.Pointer
			if isPtr {
				n = n.Elem()
			}
			params = append(params, getParam(L, n.Kind(), i))
		}
		rst := m.Func.Call(params)
		for _, v := range rst {
			L.Push(getResult(v))
		}
		return []reflect.Value{reflect.ValueOf(len(rst))}
	})
	fn.Set(res)
}
func getResult(v reflect.Value) lua.LValue {
	switch v.Kind() {
	case reflect.Int:
		return lua.LNumber(v.Interface().(int))
	case reflect.String:
		return lua.LString(v.Interface().(string))
	default:
		panic("unsupported type")
	}
}

func getParam(L *lua.LState, k reflect.Kind, pos int) reflect.Value {
	switch k {
	case reflect.Int:
		return reflect.ValueOf(L.ToInt(pos))
	case reflect.String:
		return reflect.ValueOf(L.ToString(pos))
	default:
		panic("unsupported type")
	}
}

func getIns() lua.LGFunction {
	var l func(*lua.LState) int
	return l
}

func register_global(rcvr any) *ServiceFuncs {
	service := new(ServiceFuncs)
	getType := reflect.TypeOf(rcvr)
	service.V = reflect.ValueOf(rcvr)
	k := getType.Kind()
	if k == reflect.Pointer {
		el := getType.Elem()
		sname := fmt.Sprintf("%s.%s", el.PkgPath(), el.Name())
		service.N = sname
	} else {
		sname := fmt.Sprintf("%s.%s", getType.PkgPath(), getType.Name())
		service.N = sname
	}
	// Install the methods
	service.M = suitableMethods(getType)
	return service
}

func suitableMethods(typ reflect.Type) map[string]reflect.Method {
	methods := make(map[string]reflect.Method)
	for m := 0; m < typ.NumMethod(); m++ {
		m := typ.Method(m)
		if m.PkgPath != "" {
			continue
		}
		if !m.IsExported() {
			continue
		}
		methods[m.Name] = m
	}
	return methods
}
