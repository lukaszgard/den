"""Free and used space of the root disk: a runbook in Python, standard library only."""

import shutil

GIB = 1024**3
usage = shutil.disk_usage("/")
print(f"{'disk':<8}{'size':>10}{'used':>10}{'free':>10}")
print(f"{'/':<8}{usage.total / GIB:>9.1f}G{usage.used / GIB:>9.1f}G{usage.free / GIB:>9.1f}G")
