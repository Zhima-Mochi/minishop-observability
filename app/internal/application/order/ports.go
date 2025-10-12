package order

type IDGenerator interface {
    NewID() string
}
