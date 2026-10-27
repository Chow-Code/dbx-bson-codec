import assert from 'node:assert/strict';
import {readFile,writeFile,readdir} from 'node:fs/promises';
import {createHash} from 'node:crypto';
import path from 'node:path';
const manifest=JSON.parse(await readFile('manifest.json','utf8'));
const directory=process.argv[2]||path.join('dist',manifest.version);
const targets=['darwin-arm64','darwin-x64','linux-arm64','linux-x64','windows-x64'];
const files=(await readdir(directory)).filter(name=>name.endsWith('.artifact.json')).sort();
const artifacts=[];
for(const file of files){
  const artifact=JSON.parse(await readFile(path.join(directory,file),'utf8'));
  assert(targets.includes(artifact.target),'Unsupported target');
  assert(!artifacts.some(other=>other.target===artifact.target),'Duplicate target');
  assert.equal(artifact.signingKeyId,undefined,'Candidate must be unsigned');
  const name=`${manifest.id}-${manifest.version}-${artifact.target}.dbxp`;
  assert.equal(file,name+'.artifact.json','Manifest version/identity must match asset metadata');
  assert.equal(artifact.url,`https://github.com/Chow-Code/dbx-bson-codec/releases/download/v${manifest.version}/${name}`);
  const bytes=await readFile(path.join(directory,name));
  assert.equal(artifact.size,bytes.length);assert.equal(artifact.sha256,createHash('sha256').update(bytes).digest('hex'));
  artifacts.push(artifact);
}
assert(artifacts.length>0,'No candidates');
if(process.argv.includes('--all'))assert.deepEqual(artifacts.map(a=>a.target).sort(),targets,'All five native targets must pass before publication');
artifacts.sort((a,b)=>a.target.localeCompare(b.target));
const plugin={id:manifest.id,name:manifest.name,description:manifest.description,publisher:manifest.publisher,version:manifest.version,permissions:manifest.permissions};
await writeFile(path.join(directory,'release-candidates.json'),JSON.stringify({plugin,artifacts},null,2)+'\n');
console.log(JSON.stringify({plugin,artifacts},null,2));
