// Opt-in synthetic transport assessment. Requires TestMetroLiveReleaseFixture behind Caddy.
import {chromium} from '@playwright/test';
import {writeFile} from 'node:fs/promises';
const base=process.env.METRO_RELEASE_URL??'http://127.0.0.1:18083';
const duration=Number(process.env.METRO_RELEASE_SECONDS??900)*1000;
const clients=Number(process.env.METRO_RELEASE_CLIENTS??32);
const browser=await chromium.launch({headless:true,args:['--enable-unsafe-swiftshader']});
const pages=[],contexts=[];
const envelope=data=>({data,page:{offset:0,total:data.length,limit:100,has_more:false,revision:'setup'}});
const config={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:30};
const staticStops=[{id:'metro:gtfs-rm',source_id:'rm',name:'Roma',lat:38.75,lon:-9.14,operator_id:'metro',route_ids:['metro:r']},{id:'metro:gtfs-cs',source_id:'cs',name:'Cais do Sodré',lat:38.71,lon:-9.14,operator_id:'metro',route_ids:['metro:r']}];
let setupErrors=[];
for(let n=0;n<clients;n++){
 const context=await browser.newContext({viewport:{width:n%2?390:1280,height:850},extraHTTPHeaders:{'X-Lisboa-Client-IP':`192.0.2.${n+1}`}});contexts.push(context);
 const page=await context.newPage();pages.push(page);
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async route=>{
  const path=new URL(route.request().url()).pathname;
  if(path.startsWith('/api/v1/metro/live'))return route.continue();
  let json=envelope([]);
  if(path.endsWith('/config'))json=config;
  if(path.endsWith('/operators'))json=envelope([{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#28a',status:'ok',static_status:'ok',live_updated_at:new Date().toISOString(),static_updated_at:new Date().toISOString(),plan_id:'synthetic',estimated_positions:1}]);
  if(path.endsWith('/stops'))json=envelope(staticStops);
  if(path.endsWith('/route-shapes'))json={...envelope([]),coverage:[]};
  if(path.endsWith('/metrics'))json={speed_kmh:null};
  await route.fulfill({json});
 });
 const cdp=await context.newCDPSession(page);await cdp.send('Network.enable');await cdp.send('Network.emulateNetworkConditions',{offline:false,latency:100,downloadThroughput:10_000_000,uploadThroughput:10_000_000});
 await page.addInitScript(()=>{
  window.metroRelease={latencies:[],revisions:[],errors:[],frames:0,bytes:0,reorders:0,snapshotReads:0,streamErrors:0,resets:0};
  let revision='',lastCursor=0;
  const Original=window.EventSource;
  window.EventSource=class extends Original{constructor(...args){super(...args);lastCursor=0;this.addEventListener('error',()=>window.metroRelease.streamErrors++);for(const event of ['reset','frame'])this.addEventListener(event,e=>{if(event==='reset')window.metroRelease.resets++;window.metroRelease.bytes+=new TextEncoder().encode(e.data).byteLength;const cursor=Number(e.lastEventId);if(event==='frame'&&cursor<=lastCursor)window.metroRelease.reorders++;lastCursor=cursor})}};
  const fetchOriginal=window.fetch;window.fetch=function(input,...args){if(String(input).includes('/api/v1/metro/live?'))window.metroRelease.snapshotReads++;return fetchOriginal.call(this,input,...args)};
  new MutationObserver(()=>{
   const node=document.querySelector('[data-metro-revision]');if(!node)return;const next=node.dataset.metroRevision;if(!next||next===revision)return;revision=next;
   const lag=Date.now()-Date.parse(node.dataset.metroPublishedAt);if(Number.isFinite(lag)&&lag>=0){window.metroRelease.latencies.push(lag);window.metroRelease.frames++}
  }).observe(document,{subtree:true,attributes:true,childList:true});
  window.addEventListener('error',e=>window.metroRelease.errors.push(e.message));
 });
 try{
  await page.goto(base,{waitUntil:'domcontentloaded'});
  if(n%2){await page.getByRole('button',{name:'Abrir operadores'}).click();}
  await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await page.getByRole('button',{name:'Roma Metro de Lisboa'}).click();
  await page.locator('.station-popup [data-call-id]').first().waitFor();
  if(n%2){await page.getByRole('button',{name:'Abrir comboio',exact:true}).first().click();await page.locator('.journey-timeline').waitFor();}
 }catch(error){setupErrors.push({client:n,error:String(error)});}
 if((n+1)%8===0)console.log(JSON.stringify({setup_clients:n+1,total_clients:clients,setup_errors:setupErrors.length}));
}
if(setupErrors.length){await browser.close();console.error(JSON.stringify({setupErrors}));process.exit(1)}
const started=new Date().toISOString();console.log(JSON.stringify({started,clients,duration_ms:duration,setupErrors}));
const samples=[];
const sampler=setInterval(async()=>{try{samples.push({at:new Date().toISOString(),...await(await fetch(base+'/test-runtime-metrics')).json()});}catch{}},30000);
await new Promise(resolve=>setTimeout(resolve,duration));clearInterval(sampler);
const results=await Promise.all(pages.map(p=>p.evaluate(()=>window.metroRelease).catch(e=>({errors:[String(e)],latencies:[]}))));
const latencies=results.flatMap(r=>r.latencies).sort((a,b)=>a-b);
const percentile=q=>latencies.length?latencies[Math.min(latencies.length-1,Math.floor(latencies.length*q))]:null;
const report={started,finished:new Date().toISOString(),duration_ms:duration,clients,setupErrors,network_profile:{cdp_latency_ms:100,download_bytes_per_second:10_000_000,upload_bytes_per_second:10_000_000},proxy:'Caddy encode zstd gzip; HTTP loopback; trusted synthetic client IPs',dataset:'synthetic 50-station source, 96 references, 1600 platform rows, 500 ms publication; unrelated API setup reads mocked; no upstream Metro calls',frames:results.reduce((n,r)=>n+(r.frames??0),0),event_payload_utf8_bytes:results.reduce((n,r)=>n+(r.bytes??0),0),p50_ms:percentile(.5),p95_ms:percentile(.95),p99_ms:percentile(.99),max_ms:latencies.at(-1),reorders:results.reduce((n,r)=>n+(r.reorders??0),0),browser_errors:results.flatMap(r=>r.errors??[]),runtime_samples:samples,snapshot_reads:results.reduce((n,r)=>n+(r.snapshotReads??0),0),stream_errors:results.reduce((n,r)=>n+(r.streamErrors??0),0),resets:results.reduce((n,r)=>n+(r.resets??0),0),client_frames:results.map(r=>r.frames??0)};
await writeFile(process.env.METRO_RELEASE_REPORT??'../docs/validation/metro-live-release-2026-09-28.json',JSON.stringify(report,null,2)+'\n');
await browser.close();console.log(JSON.stringify({frames:report.frames,p95_ms:report.p95_ms,setup_errors:setupErrors.length,browser_errors:report.browser_errors.length}));
if(setupErrors.length||report.browser_errors.length||report.reorders||report.p95_ms==null||report.p95_ms>1000||report.client_frames.some(n=>n<duration/2000))process.exitCode=1;
