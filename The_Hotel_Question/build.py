import re, subprocess
src=open('/tmp/the_hotel_question_novel.md').read()
# drop the Approach section (design note, not part of the book)
src=src[src.index('## Part One'):]
numwords={'One':'One','Two':'Two','Three':'Three','Four':'Four'}
out=[]
lines=src.split('\n')
i=0
def tex_escape_title(t):
    t=t.replace('&','\\&')
    return t
for ln in lines:
    m=re.match(r'^## Part (\w+): (.+)$',ln)
    if m:
        out.append('\n\\part{%s}\n'%m.group(2)); continue
    m=re.match(r'^## Notes to Part (\w+)$',ln)
    if m:
        out.append('\n\\notesheading{Notes to Part %s}\n'%m.group(1)); continue
    m=re.match(r'^### (\d+)\. (.+)$',ln)
    if m:
        n=int(m.group(1)); out.append('\n\\numbered{%d}{%s}\n'%(n,tex_escape_title(m.group(2)))); continue
    m=re.match(r'^### Interchapter: (.+)$',ln)
    if m:
        out.append('\n\\interchapter{%s}\n'%m.group(1)); continue
    if ln.strip()=='* * *':
        out.append('\n\\scenebreak\n'); continue
    if ln.strip()=='---':
        continue
    m=re.match(r'^\*(End of Part \w+|End)\*$',ln.strip())
    if m:
        out.append('\n\\partendnote{%s}\n'%m.group(1)); continue
    out.append(ln)
md='\n'.join(out)
open('body.md','w').write(md)
tex=subprocess.run(['pandoc','-f','markdown+smart-auto_identifiers','-t','latex','--wrap=preserve'],input=md,capture_output=True,text=True)
if tex.returncode: print(tex.stderr)
body=tex.stdout

body=body.replace('\\scenebreak\n\n','\\scenebreak\n\\noindent ')
# wrap notes in \small
body=re.sub(r'(\\notesheading\{[^}]*\})', r'\1\n\n{\\small', body)
body=re.sub(r'\n(\\part\{)', r'\n}\n\n\1', body)
body=body.rstrip()+'\n}\n'
# small-caps lead-in
def lead(m):
    head, text = m.group(1), m.group(2)
    words=text.split(' ')
    first=' '.join(words[:4])
    if re.search(r'[\\{}`$]|\\\\',first) or not first[0].isalpha():
        return m.group(0)
    return head+'\\textsc{'+first.lower()+'}'+text[len(first):]
body=re.sub(r'(\\(?:numbered\{\d+\}\{[^\n]*\}|interchapter\{[^\n]*\})\n\n)([^\n]+)', lead, body)
open('body.tex','w').write(body)
print(len(body))
