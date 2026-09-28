// Opt-in mixed synthetic recovery assessment. Requires TestMetroLiveReleaseFixture behind Caddy.
import {chromium} from '@playwright/test';
import {writeFile} from 'node:fs/promises';
const base=process.env.METRO_RELEASE_URL??'http://127.0.0.1:18083';
const duration=Number(process.env.METRO_RELEASE_SECONDS??900)*1000;
const smoke=process.env.METRO_RELEASE_SMOKE==='1';
const reselectOnly=process.env.METRO_RELEASE_RESELECT_ONLY==='1';
const controlEvents=[];
const control=async input=>{const event={requested_at:new Date().toISOString(),input};controlEvents.push(event);const r=await fetch(base+'/test-replay/control',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(input)});event.completed_at=new Date().toISOString();event.status=r.status;if(!r.ok)throw new Error('Replay control failed '+r.status)};
await control({mode:'positive',available:true,delay_ms:50});
const clients=Number(process.env.METRO_RELEASE_CLIENTS??32);
const browser=await chromium.launch({headless:true,args:['--enable-unsafe-swiftshader']});
const pages=[],contexts=[],phaseResults=[];let sampler,started;
try {
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
 const cdp=await context.newCDPSession(page);await cdp.send('Network.enable');await cdp.send('Network.emulateNetworkConditions',{offline:false,latency:0,downloadThroughput:10_000_000,uploadThroughput:10_000_000});
 await page.addInitScript(()=>{
  window.metroRelease={latencies:[],revisions:[],errors:[],frames:0,bytes:0,reorders:0,snapshotReads:0,streamErrors:0,resets:0,healthy:false,latest:null,fetchTimes:[],snapshotRequests:[],trace:[],healthyLatencies:[]};
  let revision='',activeSource;
  const Original=window.EventSource;
  window.EventSource=class extends Original{
   constructor(...args){super(...args);activeSource=this;let cursor=0;const metrics=window.metroRelease;
    this.addEventListener('error',()=>{metrics.streamErrors++;if(activeSource===this)metrics.healthy=false});
    for(const event of ['reset','frame'])this.addEventListener(event,e=>{
     if(event==='reset')metrics.resets++;metrics.bytes+=new TextEncoder().encode(e.data).byteLength;
     const next=Number(e.lastEventId);if(next<=cursor)metrics.reorders++;cursor=next;
     if(activeSource!==this)return;metrics.healthy=true;metrics.latest=JSON.parse(e.data);
     if(metrics.trace.length<4000)metrics.trace.push({at:Date.now(),event,cursor,revision:metrics.latest.revision,pin:metrics.latest.selected_journey_id});
    });
   }
   close(){if(activeSource===this)window.metroRelease.healthy=false;super.close()}
  };
  const fetchOriginal=window.fetch;window.fetch=function(input,...args){if(String(input).includes('/api/v1/metro/live?')){window.metroRelease.snapshotReads++;window.metroRelease.fetchTimes.push(Date.now());window.metroRelease.snapshotRequests.push({at:Date.now(),etag:args[0]?.headers?.['If-None-Match']??null})}return fetchOriginal.call(this,input,...args)};
  new MutationObserver(()=>{
   const node=document.querySelector('[data-metro-revision]');if(!node)return;const next=node.dataset.metroRevision;if(!next||next===revision)return;revision=next;
   const lag=Date.now()-Date.parse(node.dataset.metroPublishedAt);if(Number.isFinite(lag)&&lag>=0){window.metroRelease.latencies.push(lag);if(window.metroRelease.healthy)window.metroRelease.healthyLatencies.push(lag);window.metroRelease.frames++}
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
if(setupErrors.length){await browser.close();console.error(JSON.stringify({setupErrors}));throw new Error(JSON.stringify({setupErrors}))}
const measurementStarts=await Promise.all(pages.map(p=>p.evaluate(()=>{const m=window.metroRelease;for(const key of ['latencies','healthyLatencies','fetchTimes','snapshotRequests','trace'])m[key]=[];for(const key of ['frames','bytes','reorders','snapshotReads','streamErrors','resets'])m[key]=0;return new Date().toISOString()})));
started=new Date().toISOString();console.log(JSON.stringify({started,clients,duration_ms:duration,setupErrors}));
const samples=[];
sampler=setInterval(async()=>{try{samples.push({at:new Date().toISOString(),...await(await fetch(base+'/test-runtime-metrics')).json()});}catch{}},30000);
const pins=await Promise.all(pages.map(p=>p.evaluate(()=>window.metroRelease.latest?.selected_journey_id)));
const began=Date.now();
async function at(seconds){const remaining=began+seconds*1000-Date.now();if(remaining>0)await new Promise(r=>setTimeout(r,remaining))}
async function verify(name,predicate,timeout=12000){const deadline=Date.now()+timeout;let failures=[];do{
 failures=[];const dom=[];const states=await Promise.all(pages.map(p=>p.evaluate(()=>{const m=window.metroRelease;return {latest:m.latest?{selected_journey_id:m.latest.selected_journey_id,trains:m.latest.trains.map(t=>({journey_id:t.journey_id,reference:t.reference,association:t.association,reason:t.reason,vehicle_id:t.vehicle_id,calls:t.calls.map(c=>({arrival:{inferred:c.arrival.inferred??null,actual:c.arrival.actual??null,prediction:c.arrival.prediction?true:null},departure:{kind:c.departure.kind}}))}))}:null,healthy:m.healthy,reads:m.snapshotReads,fetchTimes:m.fetchTimes,snapshotRequests:m.snapshotRequests,domText:document.querySelector('.detail-panel')?.textContent??'',domRevision:document.querySelector('[data-metro-revision]')?.dataset.metroRevision}})));for(let n=0;n<pages.length;n++){const m=states[n];dom.push({client:n,revision:m.domRevision,inferred_arrival:m.domText.includes('Chegada inferida'),partial_history:m.domText.includes('Histórico parcial restaurado'),official_prediction:m.domText.includes('Previsão oficial')});if(!predicate(m,n))failures.push(n)}
 if(!failures.length){phaseResults.push({name,at:new Date().toISOString(),passed:true,dom});console.log(JSON.stringify(phaseResults.at(-1)));return}
 await new Promise(r=>setTimeout(r,500));
 }while(Date.now()<deadline);phaseResults.push({name,at:new Date().toISOString(),passed:false,clients:failures});console.error(JSON.stringify(phaseResults.at(-1)));
}
const train=(m,n)=>m.latest?.trains.find(t=>t.journey_id===pins[n]);
const times=smoke?[15,25,35,45,65,100,196,211]:[60,75,90,115,135,180,276,300];
await at(times[0]);await control({mode:'arrival'});
await verify('positive-to-zero arrival; departures remain unknown',(m,n)=>!(n%2)||m.latest?.selected_journey_id===pins[n]&&train(m,n)?.calls[0]?.arrival.inferred!=null&&train(m,n)?.calls.every(c=>c.arrival.actual==null&&c.departure.kind==='unavailable')&&m.domText.includes('Chegada inferida'));
await at(times[1]);await control({mode:'correction'});
await verify('later positive withdraws arrival without replacing journey',(m,n)=>!(n%2)||m.latest?.selected_journey_id===pins[n]&&train(m,n)?.association==='suspended'&&train(m,n)?.calls[0]?.arrival.inferred==null&&!m.domText.includes('Chegada inferida'));
await at(times[2]);await control({forget:true});
await verify('durable correction restores partial history without live continuity',(m,n)=>!(n%2)||train(m,n)?.reason==='Histórico parcial restaurado; sem continuidade atual'&&train(m,n)?.calls.length===1&&train(m,n)?.calls[0]?.arrival.inferred==null&&m.domText.includes('Histórico parcial restaurado; sem continuidade atual'));
if(!reselectOnly){
const beforeOutage=await Promise.all(pages.map(p=>p.evaluate(()=>window.metroRelease.snapshotReads)));
await at(times[3]);const outageAt=Date.now();await control({available:false});
await verify('unavailable SSE uses at least two conditional snapshots',(m,n)=>!m.healthy&&m.reads>=beforeOutage[n]+2&&(!(n%2)||m.latest?.selected_journey_id===pins[n]),15000);
await verify('snapshot attempts remain at least five seconds apart',(m)=>m.fetchTimes.filter(v=>v>=outageAt).every((v,i,a)=>i===0||v-a[i-1]>=4900)&&m.snapshotRequests.filter(r=>r.at>=outageAt).every(r=>r.etag!=null));
await at(times[4]);await control({available:true});
await verify('reconnect resets complete scoped state and preserves pin',(m,n)=>m.healthy&&(!(n%2)||m.latest?.selected_journey_id===pins[n]),35000);
const afterReconnect=await Promise.all(pages.map(p=>p.evaluate(()=>window.metroRelease.snapshotReads)));
await new Promise(r=>setTimeout(r,5500));
await verify('healthy transport stops snapshot polling',(m,n)=>m.healthy&&m.reads===afterReconnect[n]);
await at(times[5]);await control({mode:'freeze'});
await at(times[6]-2);
await verify('source expiry removes current station predictions',(m,n)=>!!(n%2)||m.latest?.trains.length>0&&m.latest.trains.every(t=>t.association==='suspended'&&t.calls.every(c=>c.arrival.prediction==null))&&m.domText.includes('0 comboios identificados')&&!m.domText.includes('Previsão oficial'));
await at(times[6]);await control({mode:'positive'});
await verify('recovery restores station inventory but preserves old vehicle journey',(m,n)=>n%2?m.latest?.selected_journey_id===pins[n]&&train(m,n)?.association==='suspended':m.latest?.trains.filter(t=>t.association==='supported').length===96&&m.domText.includes('96 comboios identificados'));
}
await at(reselectOnly?45:times[7]);
for(let n=1;n<pages.length;n+=2){const p=pages[n];await p.locator('.journey-timeline').getByRole('button',{name:'Roma',exact:true}).click();await p.waitForFunction(()=>window.metroRelease.latest?.selected_journey_id==null&&window.metroRelease.latest?.trains.some(t=>t.reference==='1'&&t.association==='supported'&&t.vehicle_id));await p.locator('.station-popup .transit-call').filter({has:p.getByText('Comboio 1',{exact:true})}).getByRole('button',{name:'Abrir comboio',exact:true}).click()}
await verify('explicit selection admits fresh journey without copied event history',(m,n)=>!(n%2)||m.latest?.selected_journey_id!==pins[n]&&m.latest?.trains.find(t=>t.journey_id===m.latest.selected_journey_id)?.reference==='1'&&m.latest?.trains.find(t=>t.journey_id===m.latest.selected_journey_id)?.calls.length===50&&m.latest.trains.find(t=>t.journey_id===m.latest.selected_journey_id)?.calls.every(c=>c.arrival.inferred==null&&c.departure.kind==='unavailable')&&m.domText.includes('Viagem inferida com suporte atual'));
await at(duration/1000);clearInterval(sampler);
const results=await Promise.all(pages.map(p=>p.evaluate(()=>window.metroRelease).catch(e=>({errors:[String(e)],latencies:[]}))));
const latencies=results.flatMap(r=>r.healthyLatencies).sort((a,b)=>a-b);
const percentile=q=>latencies.length?latencies[Math.min(latencies.length-1,Math.floor(latencies.length*q))]:null;
const report={completed:true,started,client_measurement_started_at:measurementStarts,control_events:controlEvents,finished:new Date().toISOString(),duration_ms:duration,clients,setupErrors,phase_results:phaseResults,network_profile:{cdp_latency_ms:0,application_request_delay_ms:50,application_write_delay_ms:50,qualification:'synthetic application relay; not measured packet RTT',download_bytes_per_second:10_000_000,upload_bytes_per_second:10_000_000},proxy:'Caddy encode zstd gzip; HTTP loopback; trusted synthetic client IPs',dataset:'synthetic 50-station source, 96 references, 1600 platform rows, 500 ms publication, moving synthetic Hub positions, arrival/correction, durable restore, transport loss/reconnect, 90-second source expiry and explicit reselection; unrelated API setup reads mocked; no upstream Metro calls',frames:results.reduce((n,r)=>n+(r.frames??0),0),event_payload_utf8_bytes:results.reduce((n,r)=>n+(r.bytes??0),0),p50_ms:percentile(.5),p95_ms:percentile(.95),p99_ms:percentile(.99),max_ms:latencies.at(-1),reorders:results.reduce((n,r)=>n+(r.reorders??0),0),browser_errors:results.flatMap(r=>r.errors??[]),runtime_samples:samples,snapshot_reads:results.reduce((n,r)=>n+(r.snapshotReads??0),0),stream_errors:results.reduce((n,r)=>n+(r.streamErrors??0),0),resets:results.reduce((n,r)=>n+(r.resets??0),0),client_frames:results.map(r=>r.frames??0),client_snapshot_times:results.map(r=>r.fetchTimes),client_traces:results.map(r=>r.trace),all_visible_latency_ms:results.map(r=>r.latencies),latency_population:'visible revisions after per-client measurement reset barrier while latest native SSE connection is healthy; setup and fallback changes excluded'};
await writeFile(process.env.METRO_RELEASE_REPORT??'../docs/validation/metro-live-mixed-release-2026-09-28.json',JSON.stringify(report,null,2)+'\n');
await browser.close();console.log(JSON.stringify({frames:report.frames,p95_ms:report.p95_ms,setup_errors:setupErrors.length,browser_errors:report.browser_errors.length}));
if(phaseResults.some(p=>!p.passed)||setupErrors.length||report.browser_errors.length||report.reorders||report.p95_ms==null||report.p95_ms>1000||report.client_frames.some(n=>n<duration/2000))process.exitCode=1;

} catch(error){
 const diagnostics=await Promise.all(pages.map(p=>p.evaluate(()=>{const m=window.metroRelease;return {pin:m.latest?.selected_journey_id,healthy:m.healthy,errors:m.errors,stream_errors:m.streamErrors,reads:m.snapshotReads,first_rows:[...document.querySelectorAll('.station-popup [data-call-id]')].slice(0,3).map(n=>({text:n.textContent,buttons:n.querySelectorAll('button').length})),selected_train:m.latest?.trains.find(t=>t.journey_id===m.latest.selected_journey_id),linked_trains:m.latest?.trains.filter(t=>t.vehicle_id).length}}).catch(e=>({error:String(e)}))));
 const report={completed:false,started,failed_at:new Date().toISOString(),clients,error:String(error),control_events:controlEvents,phase_results:phaseResults,diagnostics};
 await writeFile(process.env.METRO_RELEASE_REPORT??'../docs/validation/metro-live-mixed-release-2026-09-28.json',JSON.stringify(report,null,2)+'\n');console.error(String(error));process.exitCode=1;
} finally {clearInterval(sampler);await browser.close()}
