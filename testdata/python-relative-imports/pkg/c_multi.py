from . import mod, second
from . import mod as aliased


def use():
    return mod.helper() + second.other() + aliased.helper()
