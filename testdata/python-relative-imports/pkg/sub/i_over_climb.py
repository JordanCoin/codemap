# Four dots from pkg/sub climbs past the scan root; there is no package
# above it, so this must resolve to nothing rather than to a root-level file.
from ....mod import helper


def use():
    return helper()
