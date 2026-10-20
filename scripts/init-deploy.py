#!/usr/bin/env python3
"""Generate private P0 credentials, never print them or overwrite existing secrets."""
import json, secrets
from pathlib import Path
root=Path('.local/deploy');root.mkdir(parents=True,exist_ok=True);root.chmod(0o700)
for name in ('mysql_root_password','mysql_app_password'):
 p=root/name
 if not p.exists():p.write_text(secrets.token_hex(24)+'\n')
 p.chmod(0o444) # parent directory is 0700; container mysql UID must read bind-mounted secrets
password=(root/'mysql_app_password').read_text().strip()
(root/'client-env.json').write_text(json.dumps({'LOADGEN_MYSQL_DSN':f'bcl:{password}@tcp(127.0.0.1:13306)/bcl?parseTime=true','P0_MYSQL_ADDRESS':'127.0.0.1:13306','P0_MYSQL_USER':'bcl','P0_MYSQL_PASSWORD':password}))
(root/'client-env.json').chmod(0o600)
print('Private P0 credentials ready; values not displayed')
