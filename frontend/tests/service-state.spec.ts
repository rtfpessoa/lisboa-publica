import {test,expect,type Page} from '@playwright/test';
import {plate} from '../src/data';

test('plate presentation respects road modes and raw unknown registrations',()=>{
 for(const [raw,wanted] of [['CE29PV','CE-29-PV'],['ab1234','AB-12-34'],['1234ab','12-34-AB'],['12ab34','12-AB-34'],[' CE 29 PV ','CE-29-PV'],['CE-29-PV','CE-29-PV'],['foreign-123','foreign-123'],['CE29','CE29']])expect(plate(raw,'bus')).toBe(wanted);
 expect(plate('CE29PV','train')).toBe('CE29PV');expect(plate('CE29PV','ferry')).toBe('CE29PV');expect(plate(null,'bus')).toBe('Indisponível');expect(plate('','bus')).toBe('Indisponível');
});

async function fixture(page:Page,operator='carris',known=false,status:string|null='STOPPED_AT'){
 const now=Date.now(),at=new Date(now-(known?360000:0)).toISOString();
 const service={origin_source_stop_id:'O',origin_name:'Porto',destination_source_stop_id:'D',destination_name:'Faro',service_date:'2026-09-26',source_url:'https://official.example/gtfs'};
 const v={id:operator+':v',source_id:'v',operator_id:operator,position_kind:operator==='metro'?'estimated':'reported',lat:38.731,lon:-9.145,observed_at:at,collected_at:at,current_status:status,stop_name:'Oriente',license_plate:'CE29PV',source_url:'https://go.tmlmobilidade.pt',last_known:known,stale:known,inactive_at:new Date(Date.parse(at)+300000).toISOString(),last_known_expires_at:new Date(Date.parse(at)+3600000).toISOString(),...(operator==='cp'?{scheduled_service:service}:{})};
 const operators=[{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#e22'},{id:operator,name:operator==='cp'?'CP':'Carris',mode:operator==='cp'?'train':'bus',color:'#2a2'}].filter((v,i,a)=>a.findIndex(x=>x.id===v.id)===i).map(o=>({...o,status:'ok',static_status:'ok',live_updated_at:new Date(now).toISOString(),reported_positions:1,estimated_positions:1}));
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',r=>{const u=new URL(r.request().url()),p=u.pathname;let data:unknown=paged([]);
  if(p.endsWith('/operators'))data=paged(operators);
  if(p.endsWith('/vehicles'))data=paged([v]);
  if(p.endsWith('/config'))data={dev_auth:false,history_retention_days:30,history_resolution_seconds:300,history_collection_status:'collecting',live_refresh_seconds:5};
  if(p.endsWith('/me'))return r.fulfill({status:401,json:{message:'Sessão necessária'}});
  if(p.endsWith('/fleet'))data=paged([{...v,first_seen:at,last_seen:at,route_ids:[]}]);
  if(p.endsWith('/route-shapes'))data={...paged([]),coverage:[]};
  if(p.endsWith('/metro/status'))data={available:false,message:'Indisponível',lines:[]};
  if(p.endsWith('/health'))data={status:'ok'};
  if(p.endsWith('/metrics'))data={speed_kmh:null};
  return r.fulfill({json:data});
 });
}

async function openVehicle(page:Page){
 await expect(page.locator('.map')).toHaveAttribute('aria-busy','false');
 await page.waitForTimeout(300);
 const box=await page.locator('.map canvas').boundingBox();
 await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);
 await expect(page.locator('.detail-panel')).toBeVisible();
}

for(const width of [1280,390])test(`normal stopped bus, formatted plate and fleet agree at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:800});await fixture(page);await page.goto('/');await openVehicle(page);
 await expect(page.locator('.detail-panel')).toContainText('Parado em Oriente');await expect(page.locator('.detail-panel')).toContainText('CE-29-PV');await expect(page.locator('.detail-panel')).not.toContainText('Inativo');
 await page.getByRole('button',{name:'Fechar detalhes'}).click();
 await page.getByRole('button',{name:'Frota',exact:true}).last().click();await page.getByRole('button',{name:'Veículos',exact:true}).click();await expect(page.locator('.page-panel table')).toContainText('CE-29-PV');
});

test('last-known stop remains a previous statement and original clocks expire',async({page})=>{
 await fixture(page,'carris',true);await page.clock.install();await page.goto('/');await openVehicle(page);
 await expect(page.locator('.detail-panel')).toContainText('Último estado: Parado em Oriente');await expect(page.locator('.detail-panel')).toContainText('Sem atualização');await expect(page.locator('.live-metrics')).toContainText('sem atualização após 5 min');await expect(page.locator('.live-metrics')).not.toContainText('inativas');await expect(page.locator('.detail-panel')).not.toContainText('Inativo');
 await page.clock.fastForward(55*60000);await expect(page.locator('.detail-panel')).toContainText('Posição anterior expirada');
});

test('silence without stop state does not imply stopped and Metro state is estimated',async({page})=>{
 await fixture(page,'carris',true,null);await page.goto('/');await openVehicle(page);await expect(page.locator('.detail-panel')).toContainText('estado de paragem não confirmado');await expect(page.locator('.detail-panel')).not.toContainText('Parado em');
 await fixture(page,'metro');await page.reload();await openVehicle(page);await expect(page.locator('.detail-panel')).toContainText('Estado estimado: Parado');await expect(page.locator('.detail-panel')).toContainText('Não é GPS');
});

test('CP full endpoints are explicitly scheduled with realtime delay unavailable',async({page})=>{
 await fixture(page,'cp');await page.goto('/');await openVehicle(page);
 await expect(page.locator('.detail-panel')).toContainText('Serviço planeado');await expect(page.locator('.detail-panel')).toContainText('Porto → Faro');await expect(page.locator('.detail-panel')).toContainText('atraso em tempo real indisponível');await expect(page.locator('.detail-panel')).toContainText('CE29PV');
});

test('CP endpoint row labels its retained Lisbon times without claiming full journey times',async({page})=>{
 await fixture(page,'cp');
 const service={origin_source_stop_id:'O',origin_name:'Porto',destination_source_stop_id:'D',destination_name:'Faro',service_date:'2026-09-26',source_url:'https://official.example/gtfs'};
 await page.route('**/api/v1/routes?**',r=>r.fulfill({json:{data:[{id:'cp:1',operator_id:'cp',short_name:'1',long_name:'Serviço CP',color:'#278044',stop_ids:[]}],page:{limit:8,offset:0,total:1,has_more:false}}}));
 await page.route('**/api/v1/routes/cp%3A1',r=>r.fulfill({json:{id:'cp:1',operator_id:'cp',short_name:'1',long_name:'Serviço CP',color:'#278044',stops:[]}}));
 await page.route('**/api/v1/trips?**',r=>r.fulfill({json:{data:[{id:'cp:20260926:A',operator_id:'cp',route_id:'cp:1',headsign:'Faro',kind:'scheduled',planned_departure:'2026-09-26T23:00:00Z',planned_end:'2026-09-26T23:00:00Z',scheduled_service:service}],page:{limit:500,offset:0,total:1,has_more:false}}}));
 await page.goto('/');await page.getByLabel('Pesquisar carreira ou paragem').fill('1');await page.locator('.search-results button').first().click();await page.getByRole('button',{name:'Viagens planeadas',exact:true}).click();
 await expect(page.locator('.detail-panel table')).toContainText('Porto → Faro (planeado)');await expect(page.getByRole('columnheader',{name:'Primeira paragem local (hora)'})).toBeVisible();await expect(page.getByRole('columnheader',{name:'Última paragem local (hora)'})).toBeVisible();await expect(page.locator('.detail-panel')).toContainText('não aos extremos da viagem completa');
});
