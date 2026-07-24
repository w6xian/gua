package tt

type Test4 struct {
	Num4 int
}

func (t *Test4) GetNum4() int {
	return t.Num4
}
func (t *Test4) SetNum4(num int) int {
	t.Num4 = num
	return t.Num4
}
