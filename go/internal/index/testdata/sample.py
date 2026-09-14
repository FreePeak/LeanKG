class Greeter:
    def __init__(self, name):
        self.name = name

    def hello(self):
        return "hi " + self.name


def standalone(name):
    g = Greeter(name)
    return g.hello()
