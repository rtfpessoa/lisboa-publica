import {test,expect,type Page} from '@playwright/test';
async function fixture(page:Page){
 const now=Date.now(),iso=(n:number)=>new Date(now+n*1000).toISOString(),missing={kind:'unavailable',at:null,reason:'Sem previsão atual',actual:null,prediction:null,schedule:null};
 const stop={id:'metro:A',operator_id:'metro',source_id:'A',name:'Alameda',lat:38.731,lon:-9.145,route_ids:['metro:r']};
 const vehicle={id:'metro:7',source_id:'7',operator_id:'metro',route_id:'metro:r',route_name:'Linha Vermelha',position_kind:'estimated',lat:38.731,lon:-9.145,observed_at:iso(0),collected_at:iso(0),source_url:'https://official.example',stale:false,last_known:false,inactive_at:iso(300),last_known_expires_at:iso(600)};
 const arrival={...missing,kind:'prediction',at:iso(60),reason:'',prediction:{at:iso(60),source_url:'https://official.example',source_updated_at:iso(0),collected_at:iso(0),valid_until:iso(90)}};
 const call={id:'visit',journey_id:'metro:run:one',line_key:'metro:r',direction_key:'60',stop_id:stop.id,stop_name:stop.name,stop_sequence:0,destination:'Aeroporto',arrival,departure:missing,phase:'future'};
 const train={journey_id:'metro:run:one',reference:'7',route_id:'metro:r',direction_code:'60',destination:'Aeroporto',association:'supported',reason:'Associação inferida',source_updated_at:iso(0),valid_until:iso(90),next_index:0,current_index:null,vehicle_id:vehicle.id,calls:[call]};
 const frame={revision:'first',published_at:iso(0),plan_id:'plan',history_status:'collecting',status:{status:'ok',message:'Fonte oficial',checked_at:iso(0),lines:[],source_url:'https://official.example'},vehicles:[vehicle],trains:[train,{...train,journey_id:'metro:run:two',reference:'8',vehicle_id:null,calls:[{...call,id:'second',arrival:missing}]},{...train,journey_id:'metro:run:three',reference:'9',vehicle_id:null,calls:[{...call,id:'third',phase:'previous',arrival:missing}]},{...train,journey_id:'metro:run:four',reference:'10',vehicle_id:null,association:'suspended',calls:[{...call,id:'fourth',arrival:missing}]}],directions:[{line_key:'metro:r',line_name:'Linha Vermelha',color:'#e22',direction_key:'60',label:'Aeroporto',count:3},{line_key:'metro:r',line_name:'Linha Vermelha',color:'#e22',direction_key:'38',label:'São Sebastião',count:0}],selected_journey_id:null,unassociated_forecasts:[]};
 await page.addInitScript(value=>{
  const w=window as unknown as {metroFrame:typeof value;streams:MockSource[];emitMetro:(kind:string)=>void;failMetro:()=>void;holdMetro:boolean;nextJourneyId?:string};w.metroFrame=value;w.streams=[];
  class MockSource extends EventTarget{url:string;closed=false;cursor=0;onerror:(()=>void)|null=null;constructor(url:string){super();this.url=url;w.streams.push(this);setTimeout(()=>{if(!w.holdMetro)this.emit('reset')},0)}close(){this.closed=true}emit(kind:string){if(this.closed)return;const u=new URL(this.url,location.origin),f=structuredClone(w.metroFrame);f.selected_journey_id=u.searchParams.get('journey_id')??(u.searchParams.has('vehicle_id')?(w.nextJourneyId??'metro:run:one'):null);this.dispatchEvent(new MessageEvent(kind,{data:JSON.stringify(f),lastEventId:String(++this.cursor)}))}}
  Object.defineProperty(window,'EventSource',{value:MockSource});w.emitMetro=kind=>w.streams.forEach(s=>s.emit(kind));w.failMetro=()=>w.streams.filter(s=>!s.closed).forEach(s=>s.onerror?.());
 },frame);
 const requests:string[]=[],envelope=(data:unknown[])=>({data,page:{limit:100,offset:0,total:data.length,has_more:false,revision:'fixture'}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{const url=new URL(r.request().url());requests.push(url.pathname);let json:unknown=envelope([]);
  if(url.pathname.endsWith('/config'))json={dev_auth:false,live_refresh_seconds:5,history_retention_days:30,history_resolution_seconds:30};
  if(url.pathname.endsWith('/operators'))json=envelope([{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#e22',status:'ok',static_status:'ok',live_updated_at:iso(0),static_updated_at:iso(0),plan_id:'plan',estimated_positions:1}]);
  if(url.pathname.endsWith('/stops'))json=envelope([stop]);if(url.pathname.endsWith('/metrics'))json={speed_kmh:null};if(url.pathname.endsWith('/route-shapes'))json={...envelope([]),coverage:[]};
  if(url.pathname.endsWith('/metro/live'))json=await page.evaluate(()=>(window as unknown as {metroFrame:unknown}).metroFrame);
  await r.fulfill({json});
 });
 await page.goto('/');if((page.viewportSize()?.width??1280)<760)await page.getByRole('button',{name:'Abrir operadores'}).click();await page.getByLabel('Pesquisar carreira ou paragem').fill('Alameda');await page.getByRole('button',{name:'Alameda Metro de Lisboa'}).click();await expect(page.locator('.station-popup')).toContainText('Comboio 7');return {requests};
}
for(const width of [1280,390])test(`Metro inventory, countdown and shared journey at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:850});const f=await fixture(page),panel=page.locator('.detail-panel');
 await expect(panel).toContainText('A chegar a esta estação');await expect(panel).toContainText('Comboio 8');await expect(panel).toContainText('Já passou nesta estação');await expect(panel).toContainText('Dados antigos ou ambíguos');await expect(panel).toContainText('3 comboios identificados');
 const clock=panel.locator('time').first(),initial=await clock.textContent();await expect.poll(()=>clock.textContent()).not.toBe(initial);expect(f.requests.filter(p=>p.endsWith('/metro/live'))).toHaveLength(0);expect(f.requests.some(p=>p.endsWith('/board')||p.endsWith('/board/calls')||p.endsWith('/vehicles'))).toBe(false);
 await panel.getByRole('button',{name:'Abrir comboio',exact:true}).click();await expect(panel).toContainText('Viagem inferida com suporte atual');await expect(panel.locator('.journey-timeline li')).toHaveCount(1);await expect(panel).toContainText('Sem dados de partida');
 if(width===1280){await page.mouse.move(500,300);await page.mouse.down();await page.mouse.move(550,340,{steps:5});await page.mouse.up();await expect(panel.getByRole('button',{name:'Retomar seguimento'})).toBeVisible()}
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth)).toBe(true);
});
test('Metro reconnect preserves direction and pinned run without fabricated events',async({page})=>{
 const f=await fixture(page),panel=page.locator('.detail-panel');await panel.getByRole('button',{name:'São Sebastião',exact:true}).click();await page.evaluate(()=>(window as unknown as {failMetro:()=>void}).failMetro());await expect.poll(()=>f.requests.filter(p=>p.endsWith('/metro/live')).length).toBe(1);await expect(panel.getByRole('button',{name:'São Sebastião',exact:true})).toHaveAttribute('aria-pressed','true');await page.waitForTimeout(1500);
 await panel.getByRole('button',{name:'Aeroporto',exact:true}).click();await panel.getByRole('button',{name:'Abrir comboio',exact:true}).click();await page.evaluate(()=>{const w=window as unknown as {metroFrame:{revision:string;trains:{association:string;reason:string}[]};emitMetro:(s:string)=>void};w.metroFrame.revision='suspended';w.metroFrame.trains[0].association='suspended';w.metroFrame.trains[0].reason='Última viagem selecionada';w.emitMetro('frame')});await expect(panel).toContainText('Última viagem selecionada');await expect(panel.getByRole('button',{name:'A seguir comboio'})).toBeDisabled();await expect(panel).not.toContainText('Chegada inferida');
});

 test('Metro unavailable SSE falls back every five seconds and reset cancels polling',async({page})=>{
  const f=await fixture(page);
  await page.evaluate(()=>{const w=window as unknown as {holdMetro:boolean;failMetro:()=>void};w.holdMetro=true;w.failMetro()});
  await expect.poll(()=>f.requests.filter(p=>p.endsWith('/metro/live')).length).toBe(1);
  await page.waitForTimeout(3000);expect(f.requests.filter(p=>p.endsWith('/metro/live'))).toHaveLength(1);
  await expect.poll(()=>f.requests.filter(p=>p.endsWith('/metro/live')).length,{timeout:5000}).toBe(2);
  await page.evaluate(()=>{const w=window as unknown as {holdMetro:boolean;emitMetro:(kind:string)=>void};w.holdMetro=false;w.emitMetro('reset')});
  const count=f.requests.filter(p=>p.endsWith('/metro/live')).length;
  await page.waitForTimeout(5500);expect(f.requests.filter(p=>p.endsWith('/metro/live'))).toHaveLength(count);
 });

 test('Metro ordered timeline keeps inferred arrival separate and validates station navigation',async({page})=>{
  await fixture(page);await page.evaluate(()=>{
   const w=window as unknown as {metroFrame:any;emitMetro:(kind:string)=>void},train=w.metroFrame.trains[0],call=train.calls[0];
   const at=new Date(Date.now()-10000).toISOString();
   train.next_index=1;train.current_index=null;train.calls=[
    {...call,id:'previous',stop_name:'Estação anterior',phase:'previous',arrival:{kind:'inferred',at,inferred:{at,window_start:at,window_end:at,mode:'inferred_arrival',model_version:'synthetic',persistence:'committed',reason:'Synthetic publication evidence'}}},
    {...call,id:'next',stop_plan_id:'plan',phase:'future'},
    {...call,id:'remaining',stop_name:'Estação seguinte',stop_plan_id:'old-plan',phase:'future'}
   ];w.metroFrame.revision='ordered';w.emitMetro('frame');
  });
  await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();const panel=page.locator('.detail-panel');
  await expect(panel.locator('.journey-timeline li')).toHaveCount(3);await expect(panel.locator('.visit-previous')).toContainText('Chegada inferida');await expect(panel.locator('.visit-next')).toContainText('Próxima estação estimada');
  await expect(panel.getByRole('button',{name:'Estação seguinte',exact:true})).toBeDisabled();await expect(panel).not.toContainText('Partida estimada');
  await panel.getByRole('button',{name:'Alameda',exact:true}).click();await expect(panel.locator('.station-popup')).toBeVisible();
 });

 test('Explicit reselection of the same Metro vehicle releases the previous journey pin',async({page})=>{
  await page.setViewportSize({width:1280,height:1000});await fixture(page);
  await page.evaluate(()=>{const w=window as unknown as {metroFrame:any;emitMetro:(kind:string)=>void};w.metroFrame.vehicles[0].lat=38.74;w.metroFrame.revision='position';w.emitMetro('frame')});
  const panel=page.locator('.detail-panel');await panel.getByRole('button',{name:'Abrir comboio',exact:true}).click();
  await expect(panel).toContainText('Viagem inferida com suporte atual');await page.waitForTimeout(600);
  await page.evaluate(()=>{
   const w=window as unknown as {metroFrame:any;nextJourneyId:string;emitMetro:(kind:string)=>void},old=w.metroFrame.trains[0];
   const next={...old,journey_id:'metro:run:return',reason:'Nova viagem selecionada',direction_code:'38',calls:old.calls.map((c:any)=>({...c,journey_id:'metro:run:return'}))};
   old.association='suspended';old.reason='Última viagem selecionada';old.vehicle_id=null;
   w.metroFrame.trains.push(next);w.nextJourneyId=next.journey_id;w.metroFrame.revision='return';w.emitMetro('frame');
  });
  await expect(panel).toContainText('Última viagem selecionada');await expect(panel).not.toContainText('Nova viagem selecionada');
  const canvas=page.locator('.map canvas'),box=await canvas.boundingBox();if(!box)throw new Error('Map canvas missing');
  // The popup overlays the map center; dispatch directly to the canvas to isolate reselection.
  await canvas.dispatchEvent('click',{clientX:box.x+box.width/2,clientY:box.y+box.height/2,bubbles:true});
  await expect(panel).toContainText('Viagem inferida com suporte atual');await expect(panel).not.toContainText('Última viagem selecionada');await expect(panel.getByRole('button',{name:'A seguir comboio'})).toBeVisible();
 });

 test('Repeated reconnect failures cannot accelerate Metro snapshot polling',async({page})=>{
  await fixture(page);const times:number[]=[];page.on('request',r=>{if(new URL(r.url()).pathname==='/api/v1/metro/live')times.push(Date.now())});
  await page.evaluate(()=>{const w=window as unknown as {holdMetro:boolean;failMetro:()=>void};w.holdMetro=true;w.failMetro()});
  await expect.poll(()=>times.length).toBe(1);
  for(let i=0;i<3;i++){await page.waitForTimeout(1100);await page.evaluate(()=>(window as unknown as {failMetro:()=>void}).failMetro())}
  expect(times).toHaveLength(1);await expect.poll(()=>times.length,{timeout:5000}).toBe(2);expect(times[1]-times[0]).toBeGreaterThanOrEqual(4900);
 });

 test('Metro ignores duplicate and regressive cursors and closed scope events',async({page})=>{
  await fixture(page);const panel=page.locator('.detail-panel');await panel.getByRole('button',{name:'Abrir comboio',exact:true}).click();await expect(panel).toContainText('Viagem inferida com suporte atual');
  await page.evaluate(()=>{const w=window as any,s=w.streams.at(-1),f=structuredClone(w.metroFrame);f.selected_journey_id='metro:run:one';f.revision='accepted-cursor';f.trains[0].destination='Accepted destination';s.dispatchEvent(new MessageEvent('frame',{data:JSON.stringify(f),lastEventId:'100'}))});
  await expect(panel).toContainText('Accepted destination');
  await page.evaluate(()=>{const w=window as any,s=w.streams.at(-1),f=structuredClone(w.metroFrame);f.selected_journey_id='metro:run:one';f.revision='rejected-cursor';f.trains[0].destination='Rejected destination';for(const id of ['100','99'])s.dispatchEvent(new MessageEvent('frame',{data:JSON.stringify(f),lastEventId:id}));w.streams[0].dispatchEvent(new MessageEvent('reset',{data:JSON.stringify(f),lastEventId:'101'}))});
  await page.waitForTimeout(500);await expect(panel).toContainText('Accepted destination');await expect(panel).not.toContainText('Rejected destination');
 });

 test('A late fallback response cannot replace a completed Metro stream reset',async({page})=>{
  await fixture(page);await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();const panel=page.locator('.detail-panel');await expect(panel).toContainText('Viagem inferida com suporte atual');
  let release!:()=>void;const gate=new Promise<void>(r=>release=r);let entered=false;
  await page.route('**/api/v1/metro/live?**',async r=>{const f=await page.evaluate(()=>structuredClone((window as any).metroFrame));f.selected_journey_id='metro:run:one';f.revision='late-fallback';f.trains[0].destination='Late fallback destination';entered=true;await gate;await r.fulfill({json:f}).catch(()=>{})});
  await page.evaluate(()=>{const w=window as any;w.holdMetro=true;w.failMetro()});await expect.poll(()=>entered).toBe(true);
  await expect.poll(()=>page.evaluate(()=>(window as any).streams.filter((s:any)=>!s.closed).length),{timeout:8000}).toBeGreaterThan(0);await page.evaluate(()=>{const w=window as any;w.metroFrame.revision='fresh-reset';w.metroFrame.trains[0].destination='Fresh reset destination';w.emitMetro('reset')});
  await expect(panel).toContainText('Fresh reset destination');release();await page.waitForTimeout(1000);await expect(panel).toContainText('Fresh reset destination');await expect(panel).not.toContainText('Late fallback destination');
 });
