import {test,expect,type Page} from '@playwright/test';
import type {CpPrediction,Arrival} from '../src/api';

async function fixture(page:Page){
 const epoch=Date.now(),iso=(n:number)=>new Date(n).toISOString();
 const day=new Intl.DateTimeFormat('en-CA',{timeZone:'Europe/Lisbon',year:'numeric',month:'2-digit',day:'2-digit'}).format(new Date(epoch));
 let failure=false,paged=false,mismatch=false,revisionExpired=false,missingDate=false,freshPlanned=false;
 const requests:{path:string,query:URLSearchParams}[]=[];
 const operators=['carris','cm','tcb','mobi','metro','cp','ttsl','fertagus'].map(id=>({id,name:id,mode:id==='metro'?'metro':id==='cp'||id==='fertagus'?'train':id==='ttsl'?'ferry':'bus',color:'#278044',status:'ok',static_status:'ok',live_updated_at:iso(epoch),static_updated_at:iso(epoch),reported_positions:id==='cp'?1:0,estimated_positions:0,note:'',error:null}));
 const stops=operators.map(o=>({id:`${o.id}:opaque-stop`,operator_id:o.id,source_id:'opaque-stop',name:o.id==='cp'?'Lisboa Santa Apolonia':'São João',lat:38.731,lon:-9.145,route_ids:[]}));
 const prediction:CpPrediction={id:'prediction',operator_id:'cp',plan_id:'plan',source_trip_id:'cp:opaque-trip',stop_id:'cp:opaque-stop',route_id:'cp:opaque-route',stop_name:'Lisboa Santa Apolonia',route_name:'R · Linha de Sintra',destination_name:'Lisboa Santa Apolonia',service_label:'123',service_date:day,date_basis:'matched_schedule',stop_sequence:0,scheduled_at:iso(epoch+570000),expected_at:iso(epoch+600000),delay_seconds:30,source_updated_at:iso(epoch),collected_at:iso(epoch),valid_until:iso(epoch+60000),source_url:'https://go.tmlmobilidade.pt/hub/api/v1/realtime/eta/gtfs'};
 const arrival:Arrival={id:'opaque-arrival',operator_id:'cp',stop_id:prediction.stop_id,route_id:prediction.route_id,trip_id:'opaque-dated-trip',headsign:'Lisboa Santa Apolonia',kind:'scheduled',scheduled_at:prediction.scheduled_at,expected_at:null,observed_at:null,source_url:'https://go.tmlmobilidade.pt/',plan_id:'plan',source_trip_id:prediction.source_trip_id,service_date:day,stop_sequence:0,route_name:'R · Linha de Sintra'};
 const pageFor=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'shared-revision'}});
 await page.addInitScript(()=>{const original=Worker.prototype.postMessage;(window as unknown as {cpFeatures:unknown[],vehicleFeatures:unknown[]}).cpFeatures=[];(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures=[];Worker.prototype.postMessage=function(message,...args){if(message?.data?.source==='cp-arrivals'&&message?.data?.data?.features)(window as unknown as {cpFeatures:unknown[]}).cpFeatures=message.data.data.features;if(message?.data?.source==='vehicles'&&message?.data?.data?.features)(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures=message.data.data.features;return Reflect.apply(original,this,[message,...args])}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,glyphs:'https://tiles.openfreemap.org/fonts/{fontstack}/{range}.pbf',sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async route=>{
  const url=new URL(route.request().url()),path=url.pathname,query=url.searchParams;requests.push({path,query});let data:unknown=pageFor([]);
  if(path.endsWith('/config'))data={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:300};
  if(path.endsWith('/health'))data={status:'ok'};
  if(path.endsWith('/operators'))data=pageFor(operators);
  if(path.endsWith('/route-shapes'))data={...pageFor([]),coverage:[]};
  if(path.endsWith('/metrics'))data={speed_kmh:null};
  if(path.endsWith('/stops'))data=pageFor(stops.filter(s=>(query.get('operators')??'').split(',').includes(s.operator_id)));
  if(path.endsWith('/vehicles')&&(query.get('operators')??'').includes('cp'))data=pageFor([{id:'cp:train',source_id:'opaque-physical-unit',operator_id:'cp',route_id:prediction.route_id,trip_id:prediction.source_trip_id,operational_date:missingDate?null:day,plan_id:'plan',route_name:'Linha de Sintra',lat:38.731,lon:-9.145,observed_at:iso(epoch),collected_at:iso(epoch),position_kind:'reported',source_url:prediction.source_url,speed_kmh:null,bearing:null,model:null,license_plate:null,stale:false,last_known:false,inactive_at:iso(epoch+300000),last_known_expires_at:iso(epoch+3600000)}]);
  if(path.endsWith('/cp/predictions')){
   if(revisionExpired&&query.get('revision')){revisionExpired=false;return route.fulfill({status:410,json:{message:'Revisão expirada'}})}
   if(failure)return route.fulfill({status:503,json:{message:'Previsões temporariamente indisponíveis'}});
   const second={...prediction,id:'second-prediction',stop_id:'cp:other-stop',stop_sequence:2};
   data={...pageFor([prediction]),availability:{status:'ok',message:'Previsões TML · CP',source_url:prediction.source_url,collected_at:iso(epoch),published_at:iso(epoch),excluded_updates:0}};
   if(paged&&!query.get('stop_id'))data={...(data as object),data:query.get('offset')==='500'?[second]:[prediction],page:{limit:500,offset:query.get('offset')==='500'?500:0,total:2,has_more:query.get('offset')!=='500',revision:'shared-revision'}};
  }
  if(path.endsWith('/arrivals'))data=pageFor([{...arrival,headsign:freshPlanned?'Novo destino publicado':arrival.headsign,plan_id:mismatch?'old-plan':'plan'}]);
  await route.fulfill({json:data});
 });
 return {epoch,requests,setFreshPlanned:(v:boolean)=>freshPlanned=v,setFailure:(v:boolean)=>failure=v,setPaged:(v:boolean)=>paged=v,setMismatch:(v:boolean)=>mismatch=v,setExpiry:(v:boolean)=>revisionExpired=v,setMissingDate:(v:boolean)=>missingDate=v};
}

async function selectCP(page:Page){
 await page.getByRole('button',{name:'CP',exact:true}).click();await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();
}

for(const width of [1280,390])test(`CP station arrivals and readable names at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:800});const f=await fixture(page);await page.goto('/');
 if(width<760)await page.getByRole('button',{name:'Abrir operadores'}).click();await selectCP(page);
 await page.getByLabel('Pesquisar carreira ou paragem').fill('santa apolonia');await expect(page.locator('.search-results button').last()).toContainText('Lisboa Santa Apolónia');await expect(page.locator('.search-results')).not.toContainText('opaque-stop');await page.locator('.search-results button').last().click();
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Lisboa Santa Apolónia');await expect(panel.getByTestId('cp-prediction')).toHaveCount(1);await expect(panel).toContainText('Comboio 123');await expect(panel).toContainText('Desvio previsto: +0,5 min');await expect(panel).not.toContainText('Horário planeado');
 expect(f.requests.filter(r=>r.path.endsWith('/arrivals')).at(-1)?.query.get('revision')).toBe('shared-revision');
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {cpFeatures:unknown[]}).cpFeatures.length)).toBe(1);await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
 await page.getByRole('button',{name:'Fechar detalhes'}).click();await expect(panel).toHaveCount(0);
});

test('expired predictions remove station badges without removing reported trains',async({page})=>{
 const f=await fixture(page);await page.clock.install({time:new Date(f.epoch)});await page.goto('/');await selectCP(page);
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {cpFeatures:unknown[]}).cpFeatures.length)).toBe(1);
 f.setFailure(true);await page.clock.fastForward(70000);
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {cpFeatures:unknown[]}).cpFeatures.length)).toBe(0);
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures.length)).toBe(1);
 await expect(page.locator('.live-metrics .metric').first()).toContainText('1');
});

test('all prediction pages load and mismatched planned associations stay separate',async({page})=>{
 const f=await fixture(page);f.setPaged(true);f.setMismatch(true);await page.goto('/');await selectCP(page);
 await expect.poll(()=>f.requests.some(r=>r.path.endsWith('/cp/predictions')&&r.query.get('offset')==='500'&&r.query.get('revision')==='shared-revision')).toBe(true);
 await page.getByLabel('Pesquisar carreira ou paragem').fill('santa');await page.locator('.search-results button').last().click();await expect(page.locator('.detail-panel')).toContainText('Horário planeado');await expect(page.getByTestId('cp-prediction')).toHaveCount(1);
});

test('readable names apply to other operators without technical stop IDs',async({page})=>{
 await fixture(page);await page.goto('/');await page.getByLabel('Pesquisar carreira ou paragem').fill('sao joao');await expect(page.locator('.search-results button').last()).toContainText('São João');await page.locator('.search-results button').last().click();await expect(page.locator('.detail-panel h3')).toContainText('São João');await expect(page.locator('.detail-panel')).not.toContainText('opaque-stop');
 await page.keyboard.press('Escape');await expect(page.locator('.detail-panel')).toHaveCount(0);
});

test('train popup separates published service label, physical identity and upstream errors',async({page})=>{
 const f=await fixture(page);await page.goto('/');await selectCP(page);
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures.length)).toBe(1);
 await page.waitForTimeout(300);const box=await page.locator('.map canvas').boundingBox();
 await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Escolher no mapa');await expect(panel.locator('.map-targets button')).toHaveCount(2);await panel.getByRole('button',{name:'CP · opaque-physical-unit Veículo'}).click();await expect(panel).toContainText('opaque-physical-unit');await expect(panel.locator('.cp-calls')).toContainText('Comboio 123');await expect(panel.locator('.cp-calls')).toContainText('Lisboa Santa Apolónia');
 f.setFailure(true);await page.waitForTimeout(6000);await expect(panel.locator('.cp-calls')).toContainText('Previsões indisponíveis.');await expect(panel.locator('.cp-calls')).toContainText('Comboio 123');
 await page.keyboard.press('Escape');await expect(panel).toHaveCount(0);
});

test('train with no published operating date never inherits a guessed association',async({page})=>{
 const f=await fixture(page);f.setMissingDate(true);await page.goto('/');await selectCP(page);
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {vehicleFeatures:unknown[]}).vehicleFeatures.length)).toBe(1);
 await page.waitForTimeout(300);const box=await page.locator('.map canvas').boundingBox();await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);
 await page.locator('.map-targets').getByRole('button',{name:'CP · opaque-physical-unit Veículo'}).click();await expect(page.locator('.cp-calls')).toContainText('associadas a este serviço indisponíveis');await expect(page.locator('.cp-calls')).not.toContainText('Comboio 123');
});

test('prediction window uses exactly one captured clock and retries an expired collection once',async({page})=>{
 const f=await fixture(page);f.setPaged(true);f.setExpiry(true);
 await page.addInitScript(()=>{const RealDate=Date;let tick=0;class TickingDate extends RealDate {constructor(value?:string|number){super(value===undefined?RealDate.now()+tick++:value)} static now(){return RealDate.now()+tick++}};window.Date=TickingDate as DateConstructor});
 await page.goto('/');await selectCP(page);
 await expect.poll(()=>f.requests.filter(r=>r.path.endsWith('/cp/predictions')&&!r.query.get('revision')).length).toBeGreaterThanOrEqual(2);
 for(const r of f.requests.filter(r=>r.path.endsWith('/cp/predictions')))expect(Date.parse(r.query.get('to')!)-Date.parse(r.query.get('from')!)).toBe(7200000);
 await expect.poll(()=>page.evaluate(()=>(window as unknown as {cpFeatures:unknown[]}).cpFeatures.length)).toBe(1);
});

 test('station prediction failure exposes fresh planned fallback without stale merges',async({page})=>{
 const f=await fixture(page);await page.goto('/');await selectCP(page);
 await page.getByLabel('Pesquisar carreira ou paragem').fill('santa apolonia');await page.locator('.search-results button').last().click();
 const panel=page.locator('.detail-panel');await expect(panel.getByTestId('cp-prediction')).toHaveCount(1);
 f.setFreshPlanned(true);f.setFailure(true);
 await expect(panel).toContainText('Novo destino publicado',{timeout:15000});
 await expect(panel).toContainText('Horário planeado');await expect(panel.getByTestId('cp-prediction')).toHaveCount(0);
 await expect(panel).toContainText('Previsões indisponíveis');
 });
