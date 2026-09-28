import {metroVehicleFixture} from './metro-vehicle-fixture';
import {test,expect,type Page} from '@playwright/test';
import type {Operator,Vehicle,RouteShape,Health,Config,Metrics,GeometryCoverage,MetroStatus} from '../src/api';
import {positionDeadline,positionIsOld} from '../src/vehicleFreshness';
import {readFileSync} from 'node:fs';

async function continuityFixture(page:Page){
 await metroVehicleFixture(page);
 const epoch=Date.now();let phase:'current'|'missing'|'error'|'recovery'='current';let collected=epoch;let reporting=false;
 const iso=(n:number)=>new Date(n).toISOString();
 const operator=(id:string):Operator=>({id,name:id==='cp'?'CP':'Metro de Lisboa',mode:id==='cp'?'train':'metro',color:'#278044',static_source:'https://go.tmlmobilidade.pt/hub/api/v1/plans',live_source:'https://go.tmlmobilidade.pt/hub/api/v1/vehicles/positions',status:id==='cp'&&phase==='error'?'error':'ok',static_status:'ok',static_updated_at:iso(epoch),live_updated_at:iso(collected),observed_at:iso(epoch),plan_id:'plan',valid_from:null,valid_until:null,reported_positions:id==='cp'&&phase==='error'?null:id==='cp'&&(phase==='current'||phase==='recovery')?1:0,estimated_positions:0,note:'',error:id==='cp'&&phase==='error'?'Fonte indisponível':null,static_error:null,direct_status:null,direct_error:null,direct_updated_at:null,last_known_positions:id==='cp'&&(phase==='missing'||phase==='error')?1:0,last_known_truncated:false});
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'fixture'}});
 await page.addInitScript(()=>{const original=Worker.prototype.postMessage;(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures=[];Worker.prototype.postMessage=function(message,...args){if(message?.data?.source==='vehicles'&&message?.data?.data?.features)(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures=message.data.data.features;return Reflect.apply(original,this,[message,...args])}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url());let data:unknown=paged([]);
  if(u.pathname.endsWith('/health'))data={status:'ok',database:'ok'} satisfies Health;
  if(u.pathname.endsWith('/config'))data={google_client_id:null,dev_auth:false,login_nonce:'',history_collection_status:'collecting',history_storage_limit_bytes:5000000000,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300} satisfies Config;
  if(u.pathname.endsWith('/operators'))data=paged(['metro','cp'].map(operator));
  if(u.pathname.endsWith('/metro/status'))data={status:'unconfigured',checked_at:null,message:'Feed direto não configurado',source_url:'https://api.metrolisboa.pt/',lines:[]} satisfies MetroStatus;
  if(u.pathname.endsWith('/route-shapes'))data={...paged([]),coverage:[]};
  if(u.pathname.endsWith('/metrics'))data={reported_vehicles:0,estimated_vehicles:0,speed_kmh:null,distance_km:null,detected_trips:null,first_snapshot:null,revision:'fixture',from:iso(epoch-3600000),to:iso(epoch),unavailable_fields:['trip_completion_percent','frequency_minutes'],live_coverage:'complete',unavailable_live_operators:[]} satisfies Metrics;
  if(u.pathname.endsWith('/vehicles')){
   const known=phase==='missing'||phase==='error',observed=phase==='recovery'?collected:epoch;
   const vehicle:Vehicle={id:'cp:train',source_id:'train',operator_id:'cp',route_name:'Sintra',route_id:null,trip_id:null,lat:38.731,lon:-9.145,observed_at:iso(observed),collected_at:iso(observed),position_kind:'reported',source_url:'https://go.tmlmobilidade.pt/hub/api/v1/vehicles/positions',speed_kmh:known?null:10,bearing:null,model:'Publicado',license_plate:null,stale:known,plan_id:'plan',typology:null,propulsion:null,last_known:known,inactive_at:iso(observed+300000),last_known_expires_at:iso(observed+600000)};
   if(reporting)vehicle.reporting={state:phase==='error'?'unknown':phase==='missing'?'not_reporting':'reporting',reason:phase==='error'?'source_error':phase==='missing'?'missing_from_snapshot':'current',state_changed_at:iso(collected),last_observed_at:iso(observed),last_seen_at:iso(observed),persisted:true};
   data=paged((u.searchParams.get('operators')??'').includes('cp')?[vehicle]:[]);
  }
  await r.fulfill({json:data});
 });
 return {epoch,operator,enableReporting:()=>{reporting=true},setPhase:(value:typeof phase,at=epoch)=>{phase=value;collected=at}};
}

test('selected CP warns at five minutes and expires at ten even without new data',async({page})=>{
 const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));const fixture=await continuityFixture(page);await page.clock.install({time:new Date(fixture.epoch)});
 await page.goto('/');await page.getByRole('button',{name:'CP',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 const count=()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures.length);
 await expect.poll(count).toBe(1);await expect(page.locator('.live-metrics .metric').first()).toContainText('1');
 fixture.setPhase('missing');await page.clock.fastForward(6000);await expect(page.locator('.live-metrics')).toContainText('1 no mapa · 0 atualizados · 1 a aguardar sinal');await expect(page.locator('.live-metrics .metric').first()).toContainText('0');await expect.poll(count).toBe(1);
 const map=page.locator('.map canvas');await expect(map).toBeVisible();const box=await map.boundingBox();await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);await expect(page.locator('.detail-panel')).toContainText('Última posição conhecida');await expect(page.locator('.detail-panel')).toContainText('Publicado');
 await page.clock.pauseAt(new Date(fixture.epoch+60000));
 fixture.setPhase('error');await page.clock.setSystemTime(fixture.epoch+298000);await page.clock.runFor(1000);
 const markerState=()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:{properties:{icon:string,old:boolean}}[]}).vehicleFeatures.map(f=>({icon:f.properties.icon,old:f.properties.old})));
 await expect.poll(markerState).toEqual([{icon:'cp',old:false}]);
 await expect(page.locator('.detail-panel')).toContainText('Última atualização do veículo');
 await expect(page.locator('.detail-panel')).not.toContainText('Recebido pela aplicação');
 await expect(page.locator('.popup-footnotes')).not.toContainText('Sem atualização há pelo menos 5 minutos');
 await page.clock.runFor(2000);
 await expect.poll(markerState).toEqual([{icon:'cp-old',old:true}]);
 await expect(page.locator('.popup-footnotes')).toContainText('Sem atualização há pelo menos 5 minutos');
 await expect(page.locator('.vehicle-update-age')).toContainText('há 5 min');
 await expect(page.locator('.live-metrics .metric').first()).toContainText('—');
 await page.route('**/api/v1/vehicles?**',r=>r.abort());
 await page.clock.setSystemTime(fixture.epoch+599000);await page.clock.fastForward(1);await expect.poll(count).toBe(1);
 await page.clock.runFor(2000);await expect.poll(count).toBe(0);
 await expect(page.locator('.detail-panel')).toContainText('Sinal expirado');await expect(page.locator('.detail-panel')).toContainText('Publicado');
 await page.unroute('**/api/v1/vehicles?**');fixture.setPhase('recovery',fixture.epoch+602000);await page.clock.runFor(15000);
 await expect.poll(count).toBe(1);await expect(page.locator('.detail-panel')).not.toContainText('Sinal expirado');
 await expect.poll(markerState).toEqual([{icon:'cp',old:false}]);expect(errors).toEqual([]);
});


test('all eight operators age their markers and expire offline',async({page})=>{
 const fixture=await continuityFixture(page),iso=(n:number)=>new Date(n).toISOString();
 const modes:Record<string,Operator['mode']>={carris:'bus',cm:'bus',tcb:'bus',mobi:'bus',metro:'metro',cp:'train',ttsl:'ferry',fertagus:'train'};
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'fixture'}});
 await page.route('**/api/v1/operators?**',r=>r.fulfill({json:paged(Object.entries(modes).map(([id,mode])=>({...fixture.operator(id),id,mode,reported_positions:id==='metro'?0:1,estimated_positions:id==='metro'?1:0})))}));
 await page.route('**/api/v1/vehicles?**',r=>{
  const selected=(new URL(r.request().url()).searchParams.get('operators')??'').split(',');
  return r.fulfill({json:paged(Object.keys(modes).filter(id=>selected.includes(id)).map((id,i)=>({id:id+':v',source_id:'v',operator_id:id,route_name:'Published line',position_kind:id==='metro'?'estimated':'reported',lat:38.731,lon:-9.145+i*.0007,observed_at:iso(fixture.epoch),collected_at:iso(fixture.epoch),source_url:'https://official.example/positions',last_known:false,stale:false,inactive_at:iso(fixture.epoch+300000),last_known_expires_at:iso(fixture.epoch+86400000)})))});
 });
 await page.clock.install({time:new Date(fixture.epoch)});await page.goto('/');await page.getByRole('button',{name:'Todos',exact:true}).click();
 const features=()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:{properties:{icon:string,old:boolean}}[]}).vehicleFeatures);
 await expect.poll(async()=>(await features()).length).toBe(8);
 await page.clock.pauseAt(new Date(fixture.epoch+60000));
 await page.route('**/api/v1/**',r=>r.abort());
 await page.clock.setSystemTime(fixture.epoch+298000);await page.clock.runFor(1000);
 expect((await features()).every(f=>!f.properties.old)).toBe(true);
 await page.clock.runFor(2000);
 await expect.poll(async()=>(await features()).map(f=>f.properties.icon).sort()).toEqual(Object.keys(modes).map(id=>id+(id==='metro'?'-estimated':'')+'-old').sort());
 await expect(page.locator('.map')).toHaveAccessibleName(/8 veículos sem atualização/);
 await page.clock.setSystemTime(fixture.epoch+599000);await page.clock.runFor(1);
 expect((await features()).length).toBe(8);
 await page.clock.runFor(2000);await expect.poll(async()=>(await features()).length).toBe(0);
});

test('healthy repeated reports keep current counts between 90 and 180 seconds',async({page})=>{
 const fixture=await continuityFixture(page);await page.clock.install({time:new Date(fixture.epoch)});await page.goto('/');await page.getByRole('button',{name:'CP',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 fixture.setPhase('current',fixture.epoch+120000);await page.clock.fastForward(120000);await expect(page.locator('.live-metrics .metric').first()).toContainText('1');await expect(page.getByRole('button',{name:'CP',exact:true})).toContainText('1 observações');
});

test('failed operator refresh makes cached coverage unknown while retaining positions',async({page})=>{
 const fixture=await continuityFixture(page);await page.clock.install({time:new Date(fixture.epoch)});await page.goto('/');await page.getByRole('button',{name:'CP',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 await expect(page.locator('.live-metrics .metric').first()).toContainText('1');
 let failed=0;await page.route('**/api/v1/operators?**',r=>{failed++;return r.fulfill({status:503,json:{code:'unavailable',message:'Estado indisponível'}})});fixture.setPhase('missing');
 await page.clock.fastForward(6000);await expect.poll(()=>failed).toBeGreaterThan(0);await page.clock.fastForward(2000);
 await expect(page.locator('.live-metrics .metric').first()).toContainText('—');await expect(page.locator('.live-metrics .metric').first()).toContainText('Fonte não verificada');await expect(page.getByRole('button',{name:'CP',exact:true})).toContainText('Estado da fonte indisponível');
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:{properties:{stale:boolean}}[]}).vehicleFeatures.map(v=>v.properties.stale))).toEqual([true]);
 await page.unroute('**/api/v1/operators?**');fixture.setPhase('recovery',fixture.epoch+15000);await page.clock.fastForward(10000);await expect(page.locator('.live-metrics .metric').first()).toContainText('1');
});

for(const width of [1280,390])test(`rail and ferry overlays follow selected operators without live reports at width ${width}`,async({page})=>{
 await page.setViewportSize({width,height:800});const fixture=await continuityFixture(page);const now=new Date().toISOString();const requests:string[]=[];
 await page.addInitScript(()=>{const original=Worker.prototype.postMessage;(window as unknown as {networkFeatures:unknown[]}).networkFeatures=[];Worker.prototype.postMessage=function(message,...args){if(message?.data?.source==='network'&&message?.data?.data?.features)(window as unknown as {networkFeatures:unknown[]}).networkFeatures=message.data.data.features;return Reflect.apply(original,this,[message,...args])}});
 await page.route('**/api/v1/operators?**',r=>r.fulfill({json:{data:[['metro','metro'],['cp','train'],['fertagus','train'],['ttsl','ferry']].map(([id,mode])=>({...fixture.operator(id),id,mode,color:'#278044',status:id==='cp'?'error':'ok',reported_positions:id==='cp'?null:0,estimated_positions:id==='cp'?null:0,live_updated_at:now,error:id==='cp'?'Fonte indisponível':null})),page:{limit:500,offset:0,total:4,has_more:false,revision:'fixture'}}}));
 await page.route('**/api/v1/vehicles?**',r=>r.fulfill({json:{data:[],page:{limit:500,offset:0,total:0,has_more:false,revision:'fixture'}}}));
 await page.route('**/api/v1/route-shapes?**',async r=>{
  const u=new URL(r.request().url());const ids=(u.searchParams.get('operators')??'').split(',');requests.push(ids.join(','));
  await r.fulfill({json:{data:ids.map(id=>({id:id+':shape',operator_id:id,route_id:id+':1',shape_id:'s',direction_id:null,headsign:'',source_url:'https://go.tmlmobilidade.pt/hub/api/v1/plans',plan_id:'plan',color:'#278044',geometry:[[-9.16,38.72],[-9.14,38.74]],updated_at:now} satisfies RouteShape)),coverage:ids.map(operator_id=>({operator_id,status:operator_id==='cp'?'partial':'available',updated_at:now,message:operator_id==='cp'?'Percursos publicados em 36/44 carreiras.':'Percursos oficiais.'} satisfies GeometryCoverage)),page:{limit:500,offset:0,total:ids.length,has_more:false,revision:'fixture'}}});
 });
 await page.goto('/');if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await expect.poll(()=>requests.at(-1)).toBe('metro');
 await page.getByRole('button',{name:'CP',exact:true}).click();await page.getByRole('button',{name:'Fertagus',exact:true}).click();await page.getByRole('button',{name:'TTSL',exact:true}).click();await expect.poll(()=>requests.at(-1)).toBe('metro,cp,fertagus,ttsl');
 if(width<760)await page.getByRole('button',{name:'Fechar operadores'}).click();await page.getByRole('button',{name:'Camadas do mapa'}).click();for(const name of ['Linhas de metro','Percursos de autocarro','Linhas de comboio','Percursos fluviais'])await expect(page.getByRole('checkbox',{name})).toBeChecked();
 const modes=()=>page.evaluate(()=>(window as unknown as {networkFeatures:{properties:{mode:string}}[]}).networkFeatures.map(f=>f.properties.mode).sort());await expect.poll(modes).toEqual(['ferry','metro','train','train']);
 await expect(page.locator('.layer-panel')).toContainText('36/44');await page.getByRole('checkbox',{name:'Linhas de comboio'}).uncheck();await expect.poll(()=>requests.at(-1)).toBe('metro,ttsl');await expect.poll(modes).toEqual(['ferry','metro']);
 await page.getByRole('checkbox',{name:'Percursos fluviais'}).uncheck();await expect.poll(modes).toEqual(['metro']);await page.getByRole('checkbox',{name:'Linhas de comboio'}).check();await expect.poll(modes).toEqual(['metro','train','train']);
});

for(const width of [1280,390])test(`full official geometry remains responsive through operator refresh at width ${width}`,async({page})=>{
 test.skip(!process.env.GEOMETRY_BROWSER_FIXTURE,'explicitly downloaded official fixture required');
 const shapes=JSON.parse(readFileSync(process.env.GEOMETRY_BROWSER_FIXTURE!,'utf8')) as RouteShape[];
 const fixture=await continuityFixture(page);await page.setViewportSize({width,height:800});await page.clock.install({time:new Date(fixture.epoch)});
 const modes:Record<string,Operator['mode']>={carris:'bus',cm:'bus',tcb:'bus',mobi:'bus',metro:'metro',cp:'train',ttsl:'ferry',fertagus:'train'};
 let refresh=0,shapeRequests=0;const errors:string[]=[];page.on('pageerror',e=>errors.push(e.message));
 await page.addInitScript(()=>{const original=Worker.prototype.postMessage;const state={count:0,posts:0,completed:0,lastId:'',workerErrors:0};(window as unknown as {networkMeasure:typeof state}).networkMeasure=state;Worker.prototype.postMessage=function(message,...args){if(message?.data?.source==='network'&&message?.data?.data?.features){const count=message.data.data.features.length;state.count=count;state.posts++;state.lastId=message.id;const reply=(event:MessageEvent)=>{if(event.data?.type==='<response>'&&event.data.id===message.id){this.removeEventListener('message',reply);if(event.data.id===state.lastId){state.completed=count;if(event.data.error)state.workerErrors++}}};this.addEventListener('message',reply)}return Reflect.apply(original,this,[message,...args])}});
 await page.route('**/api/v1/operators?**',r=>{refresh++;return r.fulfill({json:{data:Object.entries(modes).map(([id,mode])=>({...fixture.operator(id),mode,live_updated_at:new Date(fixture.epoch+refresh*1000).toISOString()})),page:{limit:500,offset:0,total:8,has_more:false,revision:'fixture'}}})});
 await page.route('**/api/v1/route-shapes?**',r=>{shapeRequests++;const u=new URL(r.request().url()),selected=(u.searchParams.get('operators')??'').split(','),rows=shapes.filter(s=>selected.includes(s.operator_id)),offset=Number(u.searchParams.get('offset')??0),limit=Number(u.searchParams.get('limit')??500);return r.fulfill({json:{data:rows.slice(offset,offset+limit),coverage:[],page:{limit,offset,total:rows.length,has_more:offset+limit<rows.length,revision:'fixture'}}})});
 await page.goto('/');if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();
 const start=performance.now();await page.getByRole('button',{name:'Todos',exact:true}).click();const measure=()=>page.evaluate(()=>(window as unknown as {networkMeasure:{count:number,posts:number,completed:number,workerErrors:number}}).networkMeasure);
 await expect.poll(async()=>(await measure()).completed,{timeout:10000}).toBe(shapes.length);const elapsed=performance.now()-start;
 if(width<760)await page.getByRole('button',{name:'Fechar operadores'}).click();await page.getByRole('button',{name:'Camadas do mapa'}).click();await expect(page.getByRole('checkbox',{name:'Linhas de comboio'})).toBeChecked();
 const before=await measure(),requests=shapeRequests,previousRefresh=refresh;await page.clock.fastForward(30000);await expect.poll(()=>refresh).toBeGreaterThan(previousRefresh);expect((await measure()).posts).toBe(before.posts);expect(shapeRequests).toBe(requests);
 await page.getByRole('checkbox',{name:'Linhas de comboio'}).uncheck();await expect.poll(async()=>(await measure()).completed,{timeout:10000}).toBe(shapes.filter(s=>modes[s.operator_id]!=='train').length);
 const cdp=await page.context().newCDPSession(page);await cdp.send('Performance.enable');const {metrics}=await cdp.send('Performance.getMetrics');const heap=metrics.find(m=>m.name==='JSHeapUsedSize')?.value??0;
 console.log(JSON.stringify({width,variants:shapes.length,points:shapes.reduce((n,s)=>n+s.geometry.length,0),first_all_geometry_ms:Math.round(elapsed),main_js_heap_bytes:heap,operator_refreshes:refresh,unchanged_network_posts:before.posts}));expect(heap).toBeLessThan(512*1024*1024);expect((await measure()).workerErrors).toBe(0);expect(errors).toEqual([]);
});

for(const operator of ['carris','cm','tcb','mobi','metro','cp','ttsl','fertagus'])test(`${operator} warns at five minutes with a ten-minute display deadline`,()=>{
 const epoch=Date.parse('2026-09-27T10:00:00Z');
 const vehicle={operator_id:operator,observed_at:new Date(epoch).toISOString()} as Vehicle;
 const threshold=5*60000;
 expect(positionIsOld(vehicle,epoch+threshold-1)).toBe(false);
 expect(positionIsOld(vehicle,epoch+threshold)).toBe(true);
 expect(positionDeadline(vehicle)).toBe(epoch+600000);
 vehicle.last_known_expires_at=new Date(epoch+86400000).toISOString();
 expect(positionDeadline(vehicle)).toBe(epoch+600000);
 vehicle.last_known_expires_at=new Date(epoch+400000).toISOString();
 expect(positionDeadline(vehicle)).toBe(epoch+400000);
 expect(epoch+threshold).toBeLessThan(positionDeadline(vehicle));
});


test('stored reporting state warns on omission immediately and recovers without implying a physical stop',async({page})=>{
 const fixture=await continuityFixture(page);fixture.enableReporting();await page.clock.install({time:new Date(fixture.epoch)});
 await page.goto('/');await page.getByRole('button',{name:'CP',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
 const icons=()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:{properties:{icon:string}}[]}).vehicleFeatures.map(f=>f.properties.icon));
 await expect.poll(icons).toEqual(['cp']);
 fixture.setPhase('missing',fixture.epoch+6000);await page.clock.fastForward(6000);await expect.poll(icons).toEqual(['cp-old']);
 const map=page.locator('.map canvas'),box=await map.boundingBox();await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);
 await expect(page.locator('.vehicle-reporting')).toHaveText('Sem sinal atual');
 await expect(page.locator('.popup-footnotes')).toContainText('deixou de aparecer');
 await expect(page.locator('.popup-footnotes')).not.toContainText('há pelo menos 5 minutos');
 await expect(page.locator('.vehicle-status')).not.toContainText('Parado');
 fixture.setPhase('error',fixture.epoch+12000);await page.clock.fastForward(6000);
 await expect(page.locator('.vehicle-reporting')).toHaveText('Sinal por confirmar');
 await expect(page.locator('.popup-footnotes')).toContainText('não podemos confirmar');
 await expect.poll(icons).toEqual(['cp']);
 fixture.setPhase('recovery',fixture.epoch+18000);await page.clock.fastForward(6000);
 await expect(page.locator('.vehicle-reporting')).toHaveText('A receber atualizações');
 await expect(page.locator('.popup-footnotes')).not.toContainText('deixou de aparecer');
 await expect.poll(icons).toEqual(['cp']);
});
