// Mounted into a finite Job. Never use initnoprompt: init must refuse existing data.
'use strict';
const fs = require('fs');
const { spawnSync } = require('child_process');
const https = require('https');
const http = require('http');
const env = process.env;
function request(base, path, basic, apiKey, method = "GET", payload) {
  return new Promise((resolve, reject) => {
    const url = new URL(base.replace(/\/$/, '') + path);
    const headers = {};
    if (basic) headers.Authorization = 'Basic ' + (basic.includes(':') ? Buffer.from(basic).toString('base64') : basic);
    if (apiKey) headers.Authorization = 'ApiKey ' + apiKey;
    const opts = { headers, timeout: 30000, method };
    if (payload) headers["Content-Type"] = "application/json";
    if (env.MTLS === "true") {opts.cert=fs.readFileSync("/var/run/arkime-mtls/tls.crt");opts.key=fs.readFileSync("/var/run/arkime-mtls/tls.key");}
    if (env.NODE_EXTRA_CA_CERTS) opts.ca = fs.readFileSync(env.NODE_EXTRA_CA_CERTS);
    const req = (url.protocol === 'https:' ? https : http).request(url, opts, res => {
      let data = ''; res.on('data', chunk => { data += chunk; if (data.length > 16e6) req.destroy(new Error('response too large')); });
      res.on('end', () => { if (![200, 201, 404, 409].includes(res.statusCode)) return reject(new Error('database HTTP ' + res.statusCode)); try { resolve({ code: res.statusCode, body: JSON.parse(data) }); } catch (e) { reject(e); } });
    });
    req.on('timeout', () => req.destroy(new Error('database timeout'))); req.on('error', reject);
    if (payload) req.write(JSON.stringify(payload)); req.end();
  });
}
function run(command, args, childEnv = env, input) {
  if (command === '/opt/arkime/db/db.pl' && env.MTLS === 'true') args = ['--clientkey','/var/run/arkime-mtls/tls.key','--clientcert','/var/run/arkime-mtls/tls.crt', ...args];
  const r = spawnSync(command, args, { env: childEnv, input, stdio: [input ? 'pipe' : 'ignore', 'inherit', 'inherit'], timeout: 480000 });
  if (r.error || r.status !== 0) throw new Error('database/admin operation failed');
}
async function schema(base, prefix, engine, basic, apiKey) {
  prefix = prefix.replace(/_?$/, '_');
  const root = await request(base, '/', basic, apiKey);
  const actual = root.body.version?.distribution === 'opensearch' ? 'OpenSearch' : 'Elasticsearch';
  if (actual !== engine) throw new Error('database engine mismatch');
  const major = Number(root.body.version?.number?.split('.')[0]);
  if ((engine === 'OpenSearch' && ![2, 3].includes(major)) || (engine === 'Elasticsearch' && ![8, 9].includes(major))) throw new Error('unsupported database major version');
  const dbSource = fs.readFileSync('/opt/arkime/db/db.pl', 'utf8');
  const expected = Number(dbSource.match(/(?:my\s+)?\$VERSION\s*=\s*(\d+)/)?.[1]);
  if (!expected) throw new Error('cannot determine image schema contract');
  const template = await request(base, '/_template/' + prefix + 'sessions3_template', basic, apiKey);
  const current = template.body[prefix + 'sessions3_template']?.mappings?._meta?.molochDbVersion;
  const markerPath = '/' + prefix + 'operator/_doc/owner';
  let marker = await request(base, markerPath, basic, apiKey);
  const owned = marker.body._source?.uid === env.CLUSTER_UID;
  const childEnv = { ...env, ARKIME__prefix: prefix, ARKIME__elasticsearchBasicAuth: basic || '', ARKIME__elasticsearchAPIKey: apiKey || '' };
  if (current !== undefined) {
    if (Number(current) > expected) throw new Error('schema downgrade refused');
    if (!owned && env.SCHEMA_MODE !== 'Adopt' && env.UPGRADE !== 'true') throw new Error('existing schema requires explicit Adopt mode');
    if (Number(current) !== expected) {
      if (env.UPGRADE !== 'true') throw new Error('schema upgrade requires target approval');
      run('/opt/arkime/db/db.pl', ['--prefix', prefix, base, 'upgradenoprompt', '--ifneeded'], childEnv);
    }
  } else {
    if (env.SCHEMA_MODE === 'Adopt') throw new Error('cannot adopt a missing schema');
    // Reject partial/legacy installations. Fresh initialization must never erase data.
    const indices = await request(base, '/_cat/indices/' + prefix + '*?format=json', basic, apiKey);
    if (Array.isArray(indices.body) && indices.body.some(i => i.index !== prefix + 'operator')) throw new Error('existing indices without current schema; manual recovery required');
    if (!owned) {
      marker = await request(base, '/' + prefix + 'operator/_create/owner', basic, apiKey, 'PUT', { uid: env.CLUSTER_UID });
      if (marker.code === 409) throw new Error('schema owned by another cluster');
    }
    run('/opt/arkime/db/db.pl', ['--prefix', prefix, base, 'init', '--ifneeded'], childEnv);
  }
}
(async () => {
  await schema(env.DATABASE_URL, env.DATABASE_PREFIX, env.DATABASE_ENGINE, env.BOOTSTRAP_BASIC || env.ARKIME__elasticsearchBasicAuth, env.BOOTSTRAP_APIKEY || env.ARKIME__elasticsearchAPIKey);
  if (env.USERS_URL && (env.USERS_URL !== env.DATABASE_URL || env.USERS_PREFIX !== env.DATABASE_PREFIX)) {
    await schema(env.USERS_URL, env.USERS_PREFIX, env.USERS_ENGINE, env.ARKIME__usersElasticsearchBasicAuth, env.ARKIME__usersElasticsearchAPIKey);
  }
  run('/opt/arkime/bin/node', ['/opt/arkime/viewer/addUser.js', '-c', '/etc/arkime/config.ini', env.ADMIN_USER, 'Initial administrator', '-', '--admin', '--createOnly'], env, env.ADMIN_PASSWORD + '\n');
})().catch(e => { console.error(e.message); process.exit(1); });
