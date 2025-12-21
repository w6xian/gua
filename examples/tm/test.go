package tm

type Test struct {
	Num3 int
}

func (t *Test) GetNum3() int {
	return t.Num3
}
func (t *Test) SetNum3(num int) int {
	t.Num3 = num
	return t.Num3
}

type Callx struct {
	Num2 int
}

func (c *Callx) GetNum2() int {
	return c.Num2
}
