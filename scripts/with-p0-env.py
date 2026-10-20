#!/usr/bin/env python3
import json,os,subprocess,sys
from pathlib import Path
values=json.loads(Path('.local/deploy/client-env.json').read_text())
raise SystemExit(subprocess.run(sys.argv[1:],env={**os.environ,**values}).returncode)
