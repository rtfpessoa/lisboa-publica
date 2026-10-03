import {test,expect,type Page} from '@playwright/test';
import {createServer,type ServerResponse} from 'node:http';
async function fixture(page:Page,metroState?:string){
 const now=Date.now(),iso=(n:number)=>new Date(now+n*1000).toISOString(),missing={kind:'unavailable',at:null,reason:'Sem previsão atual',actual:null,prediction:null,schedule:null};
 const stop={id:'metro:A',operator_id:'metro',source_id:'A',name:'Alameda',lat:38.731,lon:-9.145,route_ids:['metro:r']};
 const vehicle={id:'metro:7',source_id:'7',operator_id:'metro',route_id:'metro:r',route_name:'Linha Vermelha',position_kind:'estimated',lat:38.731,lon:-9.145,observed_at:iso(0),collected_at:iso(0),source_url:'https://official.example',stale:false,last_known:false,inactive_at:iso(300),last_known_expires_at:iso(600)};
 const arrival={...missing,kind:'prediction',at:iso(60),reason:'',prediction:{at:iso(60),source_url:'https://official.example',source_updated_at:iso(0),collected_at:iso(0),valid_until:iso(90)}};
 const call={id:'visit',journey_id:'metro:run:one',line_key:'metro:r',direction_key:'60',stop_id:stop.id,stop_name:stop.name,stop_sequence:0,destination:'Aeroporto',arrival,departure:missing,phase:'future'};
 const train={journey_id:'metro:run:one',reference:'7',route_id:'metro:r',direction_code:'60',destination:'Aeroporto',association:'supported',direction_evidence:{state:'confirmed',reason:'Synthetic qualified direction'},reason:'Associação inferida',source_updated_at:iso(0),valid_until:iso(90),next_index:0,current_index:null,vehicle_id:vehicle.id,calls:[call]};
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
  if(url.pathname.endsWith('/operators'))json=envelope([{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#e22',status:'ok',static_status:'ok',live_updated_at:iso(0),static_updated_at:iso(0),plan_id:'plan',estimated_positions:metroState==='unavailable'?0:1,model_position_state:metroState??null,last_model_position_at:metroState==='unavailable'?iso(-3600):iso(0)}]);
  if(url.pathname.endsWith('/stops'))json=envelope([stop]);if(url.pathname.endsWith('/metrics'))json={speed_kmh:null};if(url.pathname.endsWith('/route-shapes'))json={...envelope([]),coverage:[]};
  if(url.pathname.endsWith('/metro/live'))json=await page.evaluate(()=>(window as unknown as {metroFrame:unknown}).metroFrame);
  await r.fulfill({json});
 });
 await page.goto('/');if((page.viewportSize()?.width??1280)<760)await page.getByRole('button',{name:'Abrir operadores'}).click();await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true');await page.getByLabel('Pesquisar carreira ou paragem').fill('Alameda');await page.getByRole('button',{name:'Alameda Metro de Lisboa'}).press('Enter');await expect(page.locator('.station-popup')).toContainText('Comboio 7');return {requests};
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
 const f=await fixture(page),panel=page.locator('.detail-panel');await panel.getByRole('tab',{name:'São Sebastião',exact:true}).click();await page.evaluate(()=>(window as unknown as {failMetro:()=>void}).failMetro());await expect.poll(()=>f.requests.filter(p=>p.endsWith('/metro/live')).length).toBe(1);await expect(panel.getByRole('tab',{name:'São Sebastião',exact:true})).toHaveAttribute('aria-pressed','true');await page.waitForTimeout(1500);
 await panel.getByRole('tab',{name:'Aeroporto',exact:true}).click();await panel.getByRole('button',{name:'Abrir comboio',exact:true}).click();await page.evaluate(()=>{const w=window as unknown as {metroFrame:{revision:string;trains:{association:string;reason:string}[]};emitMetro:(s:string)=>void};w.metroFrame.revision='suspended';w.metroFrame.trains[0].association='suspended';w.metroFrame.trains[0].reason='Última viagem selecionada';w.emitMetro('frame')});await expect(panel).toContainText('Última viagem selecionada');await expect(panel.getByRole('button',{name:'A seguir comboio'})).toBeDisabled();await expect(panel).not.toContainText('Chegada inferida');
});

test('Metro missing pinned recovery retains history and accepts explicit unavailable reset',async({page})=>{
 await fixture(page);await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 const panel=page.locator('.detail-panel');await expect(panel.locator('.journey-timeline li')).toHaveCount(1);
 await page.evaluate(()=>{
  const w=window as any,s=w.streams.at(-1),f=structuredClone(w.metroFrame);
  f.revision='missing-pin';f.selected_journey_id=null;f.trains=[];
  f.recovery={status:'unavailable',requested_journey_id:'metro:run:one',reason:'Histórico selecionado indisponível'};
  s.dispatchEvent(new MessageEvent('reset',{data:JSON.stringify(f),lastEventId:'100'}));
 });
 await expect(panel).toContainText('Histórico selecionado indisponível');await expect(panel.locator('.journey-timeline li')).toHaveCount(1);
 await expect(panel.getByRole('button',{name:'A seguir comboio'})).toBeDisabled();await expect(panel).not.toContainText('Viagem selecionada sem confirmação');
 await expect(panel.locator('.journey-timeline time')).toHaveCount(0);await expect(panel.locator('.journey-timeline')).toContainText('Sem previsão atual');
 await expect(panel.locator('[data-metro-revision]')).toHaveAttribute('data-metro-revision','missing-pin');
});

test('Metro ambiguous vehicle shows both forecast groups with decreasing countdowns',async({page})=>{
 await fixture(page);await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 await page.evaluate(()=>{
  const w=window as any,f=w.metroFrame,call=f.trains[0].calls[0];
  f.trains[0].association='suspended';f.trains[0].reason='Várias viagens possíveis';f.revision='ambiguous-groups';
  f.forecast_contexts=[
   {reference:'7',route_id:'metro:r',direction_code:'60',destination:'Aeroporto',status:'admissible',reason:'',calls:[{...call,id:'forecast-forward',stop_name:'Forward station'}]},
   {reference:'7',route_id:'metro:r',direction_code:'38',destination:'São Sebastião',status:'admissible',reason:'',calls:[{...call,id:'forecast-reverse',stop_name:'Reverse station'}]}
  ];w.emitMetro('frame');
 });
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Viagem por confirmar');await expect(panel).toContainText('Forward station');await expect(panel).toContainText('Reverse station');
 await expect(panel).not.toContainText('Referência contraditória');await expect(panel).not.toContainText('Sem associação segura');
 const countdown=panel.locator('[data-call-id="forecast-forward"] time'),before=await countdown.textContent();
 await expect.poll(()=>countdown.textContent()).not.toBe(before);
});

test('Metro opens the currently linked episode only after explicit action',async({page})=>{
 await fixture(page);await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 await page.evaluate(()=>{
  const w=window as any,old=w.metroFrame.trains[0];
  const current={...old,journey_id:'metro:run:new',reason:'Current episode',destination:'Current destination',calls:old.calls.map((c:any)=>({...c,id:'new-visit',journey_id:'metro:run:new'}))};
  old.association='suspended';old.vehicle_id=null;old.reason='Pinned historical episode';
  w.metroFrame.trains.push(current);w.metroFrame.revision='new-current';w.emitMetro('frame');
 });
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Pinned historical episode');await expect(panel).not.toContainText('Current destination');
 await panel.getByRole('button',{name:'Abrir viagem atual',exact:true}).click();await expect(panel).toContainText('Current destination');await expect(panel).not.toContainText('Pinned historical episode');
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

test('Metro local model evaluates every 500 ms without renewing source evidence',async({page})=>{
 await fixture(page);
 const result=await page.evaluate(async()=>{
  const {metroModelVehicles}=await import('/src/metroModelPosition.ts');
  const frame=structuredClone((window as any).metroFrame),at=Date.now();
  frame.trains[0].lifecycle={state:'active',reason:'Synthetic control'};
  frame.trains[0].model_projection={model_version:'synthetic',geometry_version:'synthetic-axis',source_updated_at:new Date(at).toISOString(),valid_until:new Date(at+3000).toISOString(),from_at:new Date(at).toISOString(),to_at:new Date(at+2000).toISOString(),from_lat:38.731,from_lon:-9.145,to_lat:38.732,to_lon:-9.144};
  const first=metroModelVehicles(frame,at),next=metroModelVehicles(frame,at+500),expired=metroModelVehicles(frame,at+3000);
  frame.trains[0].association='suspended';const unsupported=metroModelVehicles(frame,at+1000);
  return {first:first[0],next:next[0],expired:expired[0],unsupported:unsupported[0],original:frame.vehicles[0]};
 });
 expect(result.next.lat).toBeCloseTo(38.73125);expect(result.next.lon).toBeCloseTo(-9.14475);
 expect(result.next.observed_at).toBe(result.first.observed_at);expect(result.next.collected_at).toBe(result.first.collected_at);
 expect(result.expired).toEqual(result.original);expect(result.unsupported).toEqual(result.original);
});
test('Metro completed journey and withdrawn departure retain revision history',async({page})=>{
 await fixture(page);await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 await page.evaluate(()=>{
  const w=window as any,f=w.metroFrame,t=f.trains[0],c=t.calls[0];f.revision='completed-withdrawal';
  t.association='suspended';t.lifecycle={state:'completed',reason:'Chegada inferida à estação final; regresso por confirmar'};
  c.departure={...c.departure,reason:'Estimativa retirada'};
  c.departure_revisions=[{revision:1,status:'estimated',source_at:t.source_updated_at,reason:'Synthetic original model departure'},{revision:2,status:'withdrawn',source_at:t.source_updated_at,reason:'Synthetic correction'}];
  w.emitMetro('frame');
 });
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('regresso por confirmar');await expect(panel.locator('.journey-timeline')).toContainText('Estimativa retirada');
 await panel.getByText('Revisões da partida').click();await expect(panel).toContainText('Synthetic correction');await expect(panel.getByRole('button',{name:'A seguir comboio'})).toBeDisabled();
});

for(const width of [1280,390])test(`Metro native SSE preserves uncertain own-only forecasts at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:850});
 await page.addInitScript(()=>{(window as any).nativeMetroEventSource=window.EventSource});
 await fixture(page);
 await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 const frame=await page.evaluate(()=>structuredClone((window as any).metroFrame));
 const call=frame.trains[0].calls[0],now=Date.now();
 const own={at:new Date(now+40000).toISOString(),source_updated_at:new Date(now).toISOString(),valid_until:new Date(now+90000).toISOString(),model_version:'synthetic-supported-own',association_episode:'synthetic-episode'};
 const ownOnly={...call,id:'own-only',service_label:'7',arrival:{kind:'unavailable',at:null,reason:'Sem previsão atual'},own_prediction:own};
 frame.revision='native-own-only';frame.unassociated_forecasts=[ownOnly];
 frame.trains[0].direction_evidence={state:'context',reason:'Possible forecast context'};
 frame.trains[0].vehicle_id=null;
 frame.forecast_contexts=[{reference:'7',route_id:'metro:r',direction_code:'60',destination:'Aeroporto',status:'admissible',reason:'',calls:[ownOnly]}];
 const clients=new Set<ServerResponse>();
 const server=createServer((req,res)=>{
  res.writeHead(200,{'Content-Type':'text/event-stream','Cache-Control':'no-cache','Access-Control-Allow-Origin':'*'});
  const params=new URL(req.url??'/', 'http://localhost').searchParams;
  const next={...frame,selected_journey_id:params.get('journey_id')};
  res.write(`id: 1\nevent: reset\ndata: ${JSON.stringify(next)}\n\n`);
  clients.add(res);req.on('close',()=>clients.delete(res));
 });
 await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve));
 const address=server.address();if(!address||typeof address==='string')throw new Error('Native SSE server missing');
 try{
  await page.route(`http://127.0.0.1:${address.port}/**`,route=>route.continue());
  await page.evaluate(base=>{
   const w=window as any,Native=w.nativeMetroEventSource;
   Object.defineProperty(window,'EventSource',{value:class extends Native{constructor(url:string){super(base+url)}}});
   w.failMetro();
  },`http://127.0.0.1:${address.port}`);
  const panel=page.locator('.detail-panel');
  await expect(panel.locator('[data-metro-revision]')).toHaveAttribute('data-metro-revision','native-own-only',{timeout:15000});
  await expect(panel.locator('.vehicle-journey')).toBeVisible();
  await expect(panel).toContainText('Viagem por confirmar');
  const vehicleOwn=panel.locator('[data-call-id="own-only"]');
  await expect(vehicleOwn).toContainText('Nossa previsão histórica (experimental)');
  await expect(vehicleOwn).toContainText('Sem previsão atual');
  await expect(panel.locator('.vehicle-journey h4').first()).not.toContainText('→');
  const before=await vehicleOwn.locator('time').textContent();
  await expect.poll(()=>vehicleOwn.locator('time').textContent()).not.toBe(before);
  if(width<760)await page.getByRole('button',{name:'Abrir operadores'}).click();
  await page.getByLabel('Pesquisar carreira ou paragem').fill('Alameda');
  await page.getByRole('button',{name:'Alameda Metro de Lisboa'}).press('Enter');
  await expect(panel.locator('.station-popup')).toBeVisible();
  await expect(panel.locator('[data-metro-revision]')).toHaveAttribute('data-metro-revision','native-own-only');
  await expect(panel.locator('[data-call-id="own-only"]')).toContainText('Nossa previsão histórica (experimental)');
  await expect(panel.getByRole('button',{name:'Abrir comboio',exact:true})).toHaveCount(0);
 }finally{
  for(const client of clients)client.destroy();
  await new Promise<void>((resolve,reject)=>server.close(error=>error?reject(error):resolve()));
 }
});

for(const width of [1280,390])test(`Metro estimated direction, own departures and keyboard tabs at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:850});await fixture(page);
 await page.evaluate(()=>{
  const w=window as any,f=w.metroFrame,t=f.trains[0],c=t.calls[0];
  f.revision='estimated-departure';t.direction_evidence={state:'estimated',reason:'Synthetic original-clock modeled direction'};
  c.own_departure_prediction={...c.arrival.prediction,at:new Date(Date.now()+85000).toISOString(),model_version:'metro-schedule-prior-v1:synthetic'};
  w.emitMetro('frame');
 });
 const panel=page.locator('.detail-panel');await expect(panel).toContainText('Estimativa por horário (experimental)');
 const forward=panel.getByRole('tab',{name:/Aeroporto/});await forward.focus();await forward.press('ArrowRight');
 await expect(panel.getByRole('tab',{name:/São Sebastião/})).toHaveAttribute('aria-selected','true');
 await panel.getByRole('tab',{name:/São Sebastião/}).press('Home');await expect(forward).toHaveAttribute('aria-selected','true');
 await panel.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 await expect(panel).toContainText('sentido estimado');await expect(panel).toContainText('Estimativa por horário (experimental)');
});

test('Metro interpolation follows published vertices by arc length',async({page})=>{
 await fixture(page);
 const value=await page.evaluate(async()=>{
  const {metroModelVehicles}=await import('/src/metroModelPosition.ts');const f=structuredClone((window as any).metroFrame),t=f.trains[0],now=Date.now();
  t.direction_evidence.state='estimated';t.lifecycle={state:'active',reason:'Synthetic modeled lifecycle'};
  t.model_projection={model_version:'synthetic',geometry_version:'synthetic',source_updated_at:new Date(now).toISOString(),valid_until:new Date(now+10000).toISOString(),from_at:new Date(now).toISOString(),to_at:new Date(now+10000).toISOString(),from_lat:0,from_lon:0,to_lat:1,to_lon:1,geometry:[[0,0],[1,0],[1,1]]};
  return metroModelVehicles(f,now+2500)[0];
 });
 expect(value.lat).toBeCloseTo(0);expect(value.lon).toBeCloseTo(.5,2);
});

test('Metro behind visits never count down and keep their last official estimate',async({page})=>{
 await fixture(page);const panel=page.locator('.detail-panel');
 await page.evaluate(()=>{
  const w=window as any,f=w.metroFrame,train=f.trains[0],call=train.calls[0],now=Date.now();
  const at=new Date(now+60000).toISOString(),source=new Date(now-5000).toISOString(),valid=new Date(now+240000).toISOString();
  const evidence={at,source_url:'https://official.example',source_updated_at:source,collected_at:null,valid_until:valid};
  train.next_index=2;train.current_index=null;train.revision='behind';f.revision='behind';
  train.calls=[
   {...call,id:'behind-official',stop_sequence:0,stop_name:'Passada com previsão',phase:'unknown',arrival:{kind:'prediction',at,prediction:evidence},last_official_estimate:evidence},
   {...call,id:'behind-own',stop_sequence:1,stop_name:'Passada com nossa',phase:'unknown',arrival:{kind:'unavailable',at:null,reason:'',actual:null,prediction:null,schedule:null},own_prediction:{...evidence,model_version:'metro-schedule-prior-v1:synthetic'}},
   {...call,id:'next-visit',stop_sequence:3,stop_name:'Próxima',phase:'future',arrival:{kind:'prediction',at,prediction:evidence},last_official_estimate:null}
  ];
  w.emitMetro('frame');
 });
 await expect(panel.locator('[data-call-id="metro:run:one"]')).toContainText('Já passou nesta estação');
 const stationRow=panel.locator('[data-call-id="metro:run:one"]');
 await expect(stationRow.locator(':scope > div').nth(1)).not.toContainText('min');
 await expect(stationRow.locator(':scope > div').nth(2)).toContainText('Sem dados de partida');
 await expect(stationRow.locator(':scope > div').nth(2)).not.toContainText('Última previsão oficial');
 await panel.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 const behind=panel.locator('[data-call-id="behind-official"]');
 await expect(behind).toContainText('Última previsão oficial');
 await expect(behind).not.toContainText('min');
 await expect(behind.locator('.transit-call > div').nth(2)).toContainText('Sem dados de partida');
 await expect(behind.locator('.transit-call > div').nth(2)).not.toContainText('Última previsão oficial');
 const ownBehind=panel.locator('[data-call-id="behind-own"]');
 await expect(ownBehind).toContainText('Estimativa por horário (experimental)');
 await expect(ownBehind).not.toContainText('min');
 await expect(panel.locator('[data-call-id="next-visit"]')).toContainText('min');
});

test('Metro forecast groups render in published stop order',async({page})=>{
 await fixture(page);
 await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 await page.evaluate(()=>{
  const w=window as any,f=w.metroFrame,call=f.trains[0].calls[0];
  f.trains[0].association='suspended';f.trains[0].reason='Várias viagens possíveis';f.revision='group-order';
  const at=new Date(Date.now()+60000).toISOString();
  const row=(id:string,seq:number,name:string)=>({...call,id,stop_sequence:seq,stop_name:name,arrival:{kind:'prediction',at,prediction:{at,source_url:'https://official.example',source_updated_at:new Date().toISOString(),collected_at:null,valid_until:new Date(Date.now()+240000).toISOString()}}});
  f.forecast_contexts=[{reference:'7',route_id:'metro:r',direction_code:'60',destination:'Aeroporto',status:'admissible',reason:'',calls:[row('g3',3,'Terceira'),row('g1',1,'Primeira'),row('g2',2,'Segunda')]}];
  w.emitMetro('frame');
 });
 const panel=page.locator('.detail-panel');
 await expect(panel.locator('[data-call-id="g1"]')).toBeVisible();
 const order=await panel.locator('[data-call-id^="g"]').evaluateAll(els=>els.map(e=>e.getAttribute('data-call-id')));
 expect(order.join(',')).toBe('g1,g2,g3');
});

test('Metro unavailable Hub positions are labelled explicitly',async({page})=>{
 await fixture(page,'unavailable');
 await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toContainText('Sem posições estimadas do Hub');
 await page.getByRole('button',{name:'Fontes e disponibilidade'}).click();
 await expect(page.locator('.modal.sources')).toContainText('Sem posições estimadas do Hub');
});

test('Metro ambiguous contexts never highlight a next visit',async({page})=>{
 await fixture(page);
 await page.getByRole('button',{name:'Abrir comboio',exact:true}).click();
 await page.evaluate(()=>{
  const w=window as any,f=w.metroFrame,train=f.trains[0],call=train.calls[0],now=Date.now();
  const at=new Date(now+60000).toISOString();
  const evidence={kind:'prediction',at,prediction:{at,source_url:'https://official.example',source_updated_at:new Date(now-5000).toISOString(),collected_at:null,valid_until:new Date(now+240000).toISOString()}};
  train.association='suspended';train.reason='Várias viagens possíveis';train.next_index=0;f.revision='two-contexts';
  f.forecast_contexts=[
   {reference:'7',route_id:'metro:r',direction_code:'60',destination:'Aeroporto',status:'admissible',reason:'',calls:[{...call,id:'fwd',stop_name:'Forward station',arrival:evidence}]},
   {reference:'7',route_id:'metro:r',direction_code:'38',destination:'São Sebastião',status:'admissible',reason:'',calls:[{...call,id:'rev',stop_name:'Reverse station',arrival:evidence}]}
  ];
  w.emitMetro('frame');
 });
 const panel=page.locator('.detail-panel');
 await expect(panel).toContainText('Viagem por confirmar');
 await expect(panel.locator('.visit-next')).toHaveCount(0);
 await expect(panel).not.toContainText('Próxima estação estimada');
});
