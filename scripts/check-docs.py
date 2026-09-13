import re,subprocess
from pathlib import Path
from urllib.parse import unquote
files=[Path(p) for p in subprocess.check_output(['git','ls-files','-c','-o','--exclude-standard'],text=True).splitlines() if p.endswith('.md')]
errors=[];links=0;fences=0
for path in files:
 body=path.read_text();active=None
 for line in body.splitlines():
  match=re.match(r'^\s*(`{3,}|~{3,})',line)
  if match:
   token=match.group(1)
   if active is None:active=token;fences+=1
   elif token[0]==active[0] and len(token)>=len(active):active=None
 if active:errors.append(f'{path}: unbalanced fence')
 for dest in re.findall(r'\]\(([^\s)]+)(?:\s+"[^"]*")?\)',body)+re.findall(r'^\[[^\]]+\]:\s*(\S+)',body,re.M):
  dest=dest.strip('<>')
  if re.match(r'^[a-zA-Z][a-zA-Z0-9+.-]*:',dest) or dest.startswith('#'):continue
  dest=unquote(dest.split('#',1)[0].split('?',1)[0])
  if dest:
   links+=1
   if not (path.parent/dest).exists():errors.append(f'{path}: missing {dest}')
print(f'{len(files)} Markdown files, {links} local links, {fences} fenced blocks checked')
print('\n'.join(errors));raise SystemExit(bool(errors))
