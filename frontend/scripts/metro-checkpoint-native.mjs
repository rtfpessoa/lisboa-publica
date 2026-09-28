// Opt-in synthetic direct-Go smoke check; no Metro/Hub provider calls.
// Start TestMetroLiveReleaseFixture, and build the frontend before running.
import {chromium} from '@playwright/test';
import {writeFile} from 'node:fs/promises';
const base=process.env.METRO_CHECKPOINT_URL??'http://127.0.0.1:18082';
const reportPath=process.env.METRO_CHECKPOINT_REPORT??'../docs/validation/metro-association-checkpoints-2026-09-28.json';
const envelope=data=>({data,page:{offset:0,total:data.length,limit:100,has_more:false,revision:'setup'}});
const started=new Date().toISOString(),browser=await chromium.launch({headless:true,args:['--enable-unsafe-swiftshader']});
const pages=[],errors=[],httpEvents=[];
const control=async input=>{const response=await fetch(base+'/test-replay/control',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(input)});if(!response.ok)throw new Error(`Control ${response.status}`)};
try {
 await control({mode:'positive',available:true,delay_ms:0});
 // Control changes apply at the next original publication second. Repeated
 // executions must not select from the previous run's arrival/frozen state.
 const readyDeadline=Date.now()+5000;
 for(;;){const state=await(await fetch(base+'/test-replay/state')).json();if(state.mode==='positive'&&!state.pending_mode)break;if(Date.now()>readyDeadline)throw new Error('Positive replay mode did not become ready');await new Promise(resolve=>setTimeout(resolve,50))}
 for(const width of [1280,390]){
  const context=await browser.newContext({viewport:{width,height:850}}),page=await context.newPage();pages.push(page);
  page.on('response',async response=>{if(new URL(response.url()).pathname.startsWith('/api/v1/metro/live')){const entry={at:new Date().toISOString(),width,url:response.url(),status:response.status()};httpEvents.push(entry);if(entry.status>=400)entry.body=await response.text().catch(()=>null)}});
  await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
  await page.route('**/api/v1/**',async route=>{
   const path=new URL(route.request().url()).pathname;if(path.startsWith('/api/v1/metro/live'))return route.continue();
   let json=envelope([]);
   if(path.endsWith('/config'))json={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:30};
   if(path.endsWith('/operators'))json=envelope([{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#28a',status:'ok',static_status:'ok',live_updated_at:new Date().toISOString(),static_updated_at:new Date().toISOString(),plan_id:'synthetic',estimated_positions:96}]);
   if(path.endsWith('/stops'))json=envelope([{id:'metro:gtfs-rm',source_id:'rm',name:'Roma',lat:38.75,lon:-9.14,operator_id:'metro',route_ids:['metro:r']}]);
   if(path.endsWith('/route-shapes'))json={...envelope([]),coverage:[]};
   if(path.endsWith('/metrics'))json={speed_kmh:null};await route.fulfill({json});
  });
  await page.addInitScript(()=>{
   window.checkpointSmoke={latencies:[],snapshots:0,resets:0,errors:[],latest:null,events:[]};
   const log=(kind,detail={})=>window.checkpointSmoke.events.push({at:new Date().toISOString(),kind,hidden:document.hidden,...detail});
   document.addEventListener('visibilitychange',()=>log('visibility'));
   const Native=window.EventSource;window.EventSource=class extends Native{
    constructor(...args){super(...args);log('connect',{url:String(args[0])});for(const kind of ['open','error','unavailable'])this.addEventListener(kind,()=>log(kind,{url:this.url,ready_state:this.readyState}));for(const kind of ['reset','frame'])this.addEventListener(kind,e=>{const frame=JSON.parse(e.data);if(kind==='reset')window.checkpointSmoke.resets++;window.checkpointSmoke.latest=frame;log(kind,{url:this.url,selected:frame.selected_journey_id,selected_present:!frame.selected_journey_id||frame.trains.some(t=>t.journey_id===frame.selected_journey_id),recovery:frame.recovery?.status})})}
    close(){log('close',{url:this.url});super.close()}
   };
   const nativeFetch=window.fetch;window.fetch=function(input,...args){if(String(input).includes('/api/v1/metro/live?')){window.checkpointSmoke.snapshots++;log('snapshot',{url:String(input)})}return nativeFetch.call(this,input,...args)};
   let revision='';new MutationObserver(()=>{const node=document.querySelector('[data-metro-revision]');if(!node?.dataset.metroRevision||node.dataset.metroRevision===revision)return;revision=node.dataset.metroRevision;const ms=Date.now()-Date.parse(node.dataset.metroPublishedAt);if(Number.isFinite(ms)&&ms>=0)window.checkpointSmoke.latencies.push(ms)}).observe(document,{subtree:true,attributes:true,childList:true});
   window.addEventListener('error',e=>window.checkpointSmoke.errors.push(e.message));
  });
  await page.goto(base,{waitUntil:'domcontentloaded'});if(width===390)await page.getByRole('button',{name:'Abrir operadores'}).click();
  await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await page.getByRole('button',{name:'Roma Metro de Lisboa'}).click();
  await page.locator('.station-popup [data-call-id]').first().waitFor();
  if(width===390){await page.locator('.station-popup .transit-call').filter({has:page.getByText('Comboio 1',{exact:true})}).getByRole('button',{name:'Abrir comboio',exact:true}).click();await page.locator('.journey-timeline').waitFor()}
 }
 await new Promise(resolve=>setTimeout(resolve,8000));
 const vehicle=pages[1],pin=await vehicle.evaluate(()=>window.checkpointSmoke.latest.selected_journey_id);
 if(!pin)throw new Error('No durable selected baseline');
 const reference=await vehicle.evaluate(()=>{const f=window.checkpointSmoke.latest;return f.trains.find(t=>t.journey_id===f.selected_journey_id)?.reference});
 if(reference!=='1')throw new Error('The arrival control is bound to reference 1');
 await control({mode:'arrival'});await vehicle.getByText('Chegada inferida',{exact:true}).first().waitFor();
 await new Promise(resolve=>setTimeout(resolve,2200));await control({mode:'freeze',forget:true});
 await vehicle.getByText('Último estado confirmado restaurado; continuidade atual não comprovada',{exact:true}).waitFor();
 const recovered=await vehicle.evaluate(()=>window.checkpointSmoke.latest);
 if(recovered.selected_journey_id!==pin||!recovered.trains.some(t=>t.journey_id===pin&&t.association==='suspended'&&t.calls.length===50&&t.current_index==null&&t.next_index==null))throw new Error('Pinned checkpoint/continuity recovery failed');
 const results=await Promise.all(pages.map(p=>p.evaluate(()=>window.checkpointSmoke))),latencies=results.flatMap(r=>r.latencies).sort((a,b)=>a-b);
 const p95=latencies[Math.min(latencies.length-1,Math.floor(latencies.length*.95))]??null;
 const report={started,finished:new Date().toISOString(),scope:'Synthetic direct Go + compiled frontend + native browser EventSource; two viewports; no proxy/network-delay profile or provider requests',pinned_checkpoint_recovery:true,clients:2,changed_dom_samples:latencies.length,p95_ms:p95,resets:results.map(r=>r.resets),snapshot_reads:results.map(r=>r.snapshots),browser_errors:results.flatMap(r=>r.errors),runtime:await(await fetch(base+'/test-runtime-metrics')).json(),baseline_admission_latency:'not measured; browser setup occurred after first baseline flush',physical_accuracy:'not measured',native_events:results.map(r=>r.events),http_events:httpEvents};
 await writeFile(reportPath,JSON.stringify(report,null,2)+'\n');console.log(JSON.stringify({...report,native_events:undefined,http_events:undefined}));
 if(report.browser_errors.length||report.snapshot_reads.some(n=>n!==0)||p95==null||p95>1000)throw new Error('Native smoke gate failed');
} catch(error){
 errors.push(String(error));
 const failure={started,finished:new Date().toISOString(),errors,http_events:httpEvents,clients:await Promise.all(pages.map(p=>p.evaluate(()=>window.checkpointSmoke).catch(()=>null)))};
 await writeFile(reportPath+'.failure.json',JSON.stringify(failure,null,2)+'\n');console.error(errors.join('\n'));process.exitCode=1;
} finally {await browser.close()}
