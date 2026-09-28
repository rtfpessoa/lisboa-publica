import {metroVehicleFixture} from './metro-vehicle-fixture';
import {test,expect,type Page} from '@playwright/test';
async function fixture(page:Page){
 await metroVehicleFixture(page);
 const now=new Date().toISOString();
 const operators=[{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#ec493a',reported_positions:0,estimated_positions:2},{id:'carris',name:'Carris',mode:'bus',color:'#f5b800',reported_positions:1,estimated_positions:0},{id:'cp',name:'CP',mode:'train',color:'#278044',reported_positions:0,estimated_positions:0}].map(o=>({...o,status:'ok',static_status:'ok',live_updated_at:now}));
 const rows=[{operator_id:'metro',vehicles:2,reported_vehicles:0,estimated_vehicles:2,speed_samples:0,model_vehicles:0,plate_vehicles:0,typology_vehicles:0},{operator_id:'carris',vehicles:1,reported_vehicles:1,estimated_vehicles:0,speed_samples:3,model_vehicles:1,plate_vehicles:1,typology_vehicles:1},{operator_id:'cp',vehicles:1,reported_vehicles:1,estimated_vehicles:0,speed_samples:0,model_vehicles:0,plate_vehicles:0,typology_vehicles:0}];
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'test'}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url()),path=u.pathname;let data:unknown=paged([]);
  if(path.endsWith('/config'))data={dev_auth:false,history_retention_days:30,history_resolution_seconds:300,history_collection_status:'collecting',live_refresh_seconds:5};
  if(path.endsWith('/health'))data={status:'ok'};
  if(path.endsWith('/metro/status'))data={available:false,message:'Fonte indisponível',lines:[]};
  if(path.endsWith('/me'))return r.fulfill({status:401,json:{message:'Sessão necessária'}});
  if(path.endsWith('/operators'))data=paged(operators);
  if(path.endsWith('/operator-coverage'))data=paged(rows);
  if(path.endsWith('/metrics'))data={speed_kmh:null,first_snapshot:now};
  if(path.endsWith('/fleet'))data=paged([{id:'carris:1',source_id:'1',operator_id:'carris',model:'Volvo B7R',license_plate:'AB12CD',typology:'3.3',propulsion:'8',first_seen:now,last_seen:now,position_kind:'reported',route_ids:[]}]);
  if(path.endsWith('/route-shapes')){const selected=(u.searchParams.get('operators')??'').split(',').filter(Boolean);data={...paged(selected.map(id=>({id:id+':shape',operator_id:id,route_id:id+':1',shape_id:'1',color:'#f5b800',plan_id:'test',geometry:[[-9.16,38.72],[-9.14,38.73]],updated_at:now}))),coverage:selected.map(operator_id=>({operator_id,status:'available',message:'GTFS'}))}}
  await r.fulfill({json:data});
 });
}
test('Metro alone and overlays enabled by default, limited to selected modes',async({page})=>{
 await fixture(page);const requested:string[]=[];page.on('request',r=>{if(r.url().includes('/route-shapes?'))requested.push(new URL(r.url()).searchParams.get('operators')!)});
 await page.goto('/');await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true');await expect(page.locator('.main-operator').first()).toHaveAttribute('aria-pressed','false');
 await page.getByRole('button',{name:'Camadas do mapa'}).click();await expect(page.getByRole('checkbox',{name:'Linhas de metro'})).toBeChecked();await expect(page.getByRole('checkbox',{name:'Percursos de autocarro'})).toBeChecked();await expect.poll(()=>requested.at(-1)).toBe('metro');
 await page.locator('.main-operator').first().click();await expect.poll(()=>requested.at(-1)).toBe('metro,carris');await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await expect.poll(()=>requested.at(-1)).toBe('carris');
});
test('traffic shows loading without a false empty result',async({page})=>{
 await fixture(page);let release:()=>void=()=>{};const gate=new Promise<void>(r=>release=r);
 await page.route('**/api/v1/traffic?**',async r=>{await gate;await r.fulfill({json:{data:[],page:{limit:500,offset:0,total:0,has_more:false,revision:'test'}}})});
 await page.goto('/');await page.getByRole('button',{name:'Trânsito',exact:true}).click();await expect(page.locator('.traffic-panel')).toHaveAttribute('aria-busy','true');await expect(page.locator('.traffic-panel [role="status"]')).toContainText('A carregar');await expect(page.locator('.traffic-panel')).not.toContainText('0 células');release();
 await expect(page.locator('.traffic-panel')).toHaveAttribute('aria-busy','false');await expect(page.locator('.traffic-panel')).toContainText('Metro');
});
test('operator gray state follows each view rather than current positions only',async({page})=>{
 await fixture(page);await page.goto('/');const cp=page.getByRole('button',{name:'CP',exact:true}),metro=page.getByRole('button',{name:'Metro de Lisboa',exact:true});
 await expect(cp).toHaveClass(/no-data/);await cp.click();await expect(cp).toHaveAttribute('aria-pressed','true');
 await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(cp).not.toHaveClass(/no-data/);
 await page.getByRole('button',{name:'Trânsito',exact:true}).click();await expect(cp).toHaveClass(/no-data/);await expect(metro).toHaveClass(/no-data/);await expect(metro).toContainText('estimadas');
});
test('unsupported metrics explain absence and published fleet fields stay visible',async({page})=>{
 await fixture(page);await page.goto('/');await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(page.getByRole('button',{name:'% Viagens',exact:true})).toBeDisabled();await expect(page.getByRole('button',{name:'Frequência',exact:true})).toBeDisabled();await expect(page.locator('.page-panel')).toContainText('partidas e chegadas');
 await page.getByRole('button',{name:'Frota',exact:true}).click();await page.getByRole('button',{name:'Veículos',exact:true}).click();await expect(page.locator('.page-panel table')).toContainText('3.3');await expect(page.locator('.page-panel table')).toContainText('8');await expect(page.locator('.page-panel')).toContainText('metadados');
});

test('cold source loading and failed searches are not shown as stale or empty',async({page})=>{
 await fixture(page);
 await page.route('**/api/v1/operators?**',r=>r.fulfill({json:{data:[{id:'metro',mode:'metro',status:'loading',static_status:'loading'}],page:{limit:500,offset:0,total:1,has_more:false}}}));
 await page.route('**/api/v1/routes?**',r=>r.fulfill({status:503,json:{message:'Pesquisa indisponível'}}));
 await page.route('**/api/v1/stops?**',r=>r.fulfill({status:503,json:{message:'Pesquisa indisponível'}}));
 await page.goto('/');await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toContainText('A carregar fonte');
 await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await expect(page.locator('.search-results [role="alert"]')).toContainText('Pesquisa indisponível');await expect(page.locator('.search-results')).not.toContainText('Sem resultados');
});

for(const viewport of [{width:1280,height:720},{width:390,height:667}])test(`search rows remain readable and clickable at ${viewport.width}x${viewport.height}`,async({page})=>{
 await fixture(page);await page.setViewportSize(viewport);
 await page.route('**/api/v1/stops?**',async r=>{if(!new URL(r.request().url()).searchParams.get('q'))return r.fallback();const data=Array.from({length:8},(_,i)=>({id:`metro:${i}`,source_id:String(i),operator_id:'metro',name:`Roma ${i+1}`,lat:38.74,lon:-9.14,route_ids:[]}));await r.fulfill({json:{data,page:{limit:8,offset:0,total:8,has_more:false,revision:'test'}}})});
 await page.goto('/');if(viewport.width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');
 const results=page.locator('.search-results'),last=results.getByRole('button',{name:/Roma 8/});await expect(last).toBeAttached();await expect.poll(()=>results.evaluate(e=>e.clientHeight)).toBeGreaterThan(100);
 await last.scrollIntoViewIfNeeded();await expect.poll(()=>last.evaluate(e=>{const r=e.getBoundingClientRect();return !!document.elementFromPoint(r.x+r.width/2,r.y+r.height/2)?.closest('.search-results button')})).toBe(true);
 await last.click();await expect(page.locator('.detail-panel')).toContainText('Roma 8');
});

test('search with no selected operators explains selection and sends no search calls',async({page})=>{
 await fixture(page);const searches:string[]=[];page.on('request',r=>{const u=new URL(r.url());if(u.pathname.startsWith('/api/v1/')&&u.searchParams.has('q'))searches.push(u.href)});
 await page.goto('/');await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await expect(page.locator('.search-results')).toContainText('Selecione pelo menos um operador para pesquisar');expect(searches).toEqual([]);await expect(page.locator('.search-results')).not.toContainText('Sem resultados');
});

test('unsupported controls disclose application limits and depot explanation ignores pending fleet',async({page})=>{
 await fixture(page);let release:()=>void=()=>{};const gate=new Promise<void>(r=>release=r);
 await page.route('**/api/v1/fleet?**',async r=>{await gate;await r.fulfill({status:503,json:{message:'Frota indisponível'}})});
 await page.goto('/');await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(page.getByRole('button',{name:'% Viagens',exact:true})).toContainText('Não suportado');await expect(page.getByRole('button',{name:'Frequência',exact:true})).toContainText('Não suportado');await expect(page.locator('.page-panel')).toContainText('Não suportadas nesta aplicação');
 await page.getByRole('button',{name:'Frota',exact:true}).click();await page.getByRole('button',{name:'Estações de recolha',exact:true}).click();await expect(page.locator('.page-panel')).toContainText('Não suportadas nesta aplicação');await expect(page.locator('.page-panel')).toContainText('paragens de passageiros');release();await expect(page.locator('.page-panel')).toHaveAttribute('aria-busy','false');await expect(page.getByRole('alert').first()).toContainText('Frota indisponível');await expect(page.locator('.page-panel')).toContainText('Não suportadas nesta aplicação');
});

test('Metro speed is unsupported across views while reported operators retain temporary empty states',async({page})=>{
 await fixture(page);const now=new Date().toISOString();await page.route('**/api/v1/history?**',r=>r.fulfill({json:{data:[{bucket:now,estimated_vehicles:2,reported_vehicles:0,speed_kmh:null}],page:{limit:500,offset:0,total:1,has_more:false}}}));
 await page.goto('/');await expect(page.locator('.live-metrics')).toContainText('Não suportada para o Metro');await page.getByRole('button',{name:'Velocidade amostral',exact:true}).click();await expect(page.getByRole('dialog',{name:'Gráfico expandido'})).toContainText('Não suportada para o Metro');await expect(page.getByRole('dialog')).not.toContainText('Ainda sem pares');await page.getByRole('button',{name:'Fechar gráfico'}).click();
 await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(page.locator('.history-layout')).toContainText('Não suportada para o Metro');await expect(page.getByRole('button',{name:'Velocidade',exact:true})).toBeDisabled();await page.getByRole('button',{name:'Trânsito',exact:true}).click();await expect(page.locator('.traffic-panel')).toContainText('Não suportada para o Metro');await expect(page.locator('.traffic-panel')).not.toContainText('Sem pares de observações');
 await page.getByRole('button',{name:'Metro de Lisboa',exact:true}).click();await page.locator('.main-operator').first().click();await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(page.getByRole('button',{name:'Velocidade',exact:true})).toBeEnabled();await expect(page.locator('.history-layout')).toContainText('Ainda sem pares de observações válidos');await expect(page.locator('.history-layout')).not.toContainText('Não suportada para o Metro');
});

test('mixed selection charts valid reported speed and explains Metro exclusion',async({page})=>{
 await fixture(page);const now=new Date().toISOString();await page.route('**/api/v1/history?**',r=>r.fulfill({json:{data:[{bucket:now,estimated_vehicles:2,reported_vehicles:1,speed_kmh:18}],page:{limit:500,offset:0,total:1,has_more:false}}}));
 await page.goto('/');await page.locator('.main-operator').first().click();await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(page.locator('.history-layout .recharts-line-dots circle').first()).toBeVisible();await expect(page.locator('.history-layout')).toContainText('Metro excluído');await expect(page.locator('.history-layout')).not.toContainText('Não suportada para o Metro');
});

test('speed capability follows a Metro route filter within a mixed operator selection',async({page})=>{
 await fixture(page);await page.route('**/api/v1/routes?**',r=>r.fulfill({json:{data:[{id:'metro:1',operator_id:'metro',short_name:'Azul',long_name:'Linha Azul',color:'#00a',stop_ids:[]}],page:{limit:8,offset:0,total:1,has_more:false}}}));await page.route('**/api/v1/routes/*',r=>r.fulfill({json:{id:'metro:1',operator_id:'metro',short_name:'Azul',long_name:'Linha Azul',color:'#00a',stops:[]}}));
 await page.goto('/');await page.locator('.main-operator').first().click();await page.getByLabel('Pesquisar carreira ou paragem').fill('Azul');await page.locator('.search-results button').first().click();await expect(page.locator('.live-metrics')).toContainText('Não suportada para o Metro');await page.getByRole('button',{name:'Histórico',exact:true}).click();await expect(page.locator('.history-layout')).toContainText('Não suportada para o Metro');await expect(page.getByRole('button',{name:'Velocidade',exact:true})).toBeDisabled();
});

test('estimated Metro vehicle detail omits routine estimation and speed warnings',async({page})=>{
 await fixture(page);const now=new Date().toISOString();await page.route('**/api/v1/vehicles?**',r=>r.fulfill({json:{data:[{id:'metro:1',source_id:'1',operator_id:'metro',position_kind:'estimated',lat:38.731,lon:-9.145,observed_at:now,collected_at:now,source_url:'https://go.tmlmobilidade.pt',stale:false,speed_kmh:null}],page:{limit:500,offset:0,total:1,has_more:false}}}));
 await page.goto('/');await expect(page.locator('.map')).toHaveAttribute('aria-busy','false');await page.waitForTimeout(300);const box=await page.locator('.map canvas').boundingBox();await page.mouse.click(box!.x+box!.width/2,box!.y+box!.height/2);await expect(page.locator('.detail-panel')).toBeVisible();await expect(page.locator('.detail-panel')).not.toContainText('A velocidade amostral não é suportada');await expect(page.locator('.vehicle-update-age')).toBeVisible();await expect(page.locator('.detail-panel')).not.toContainText('Velocidade amostral');
});


test('busy reads honor Retry-After and recover without losing fleet data',async({page})=>{
 await fixture(page);await page.clock.install();const calls:number[]=[];
 await page.route('**/api/v1/fleet?**',async r=>{calls.push(Date.now());if(calls.length<3)await r.fulfill({status:503,headers:{'Retry-After':'2'},json:{code:'busy',message:'Pedidos em curso'}});else await r.fallback()});
 await page.goto('/');await page.getByRole('button',{name:'Frota',exact:true}).click();await page.getByRole('button',{name:'Veículos',exact:true}).click();
 await expect.poll(()=>calls.length).toBe(1);await page.clock.fastForward(1500);expect(calls).toHaveLength(1);
 await page.clock.fastForward(1100);await expect.poll(()=>calls.length).toBe(2);
 await page.clock.fastForward(2600);await expect(page.locator('.page-panel table')).toContainText('AB-12-CD');expect(calls).toHaveLength(3);
 await expect(page.locator('.page-panel')).not.toContainText('Frota indisponível devido a erro.');
});

test('persistent busy reads stop after bounded retries and expose an error',async({page})=>{
 await fixture(page);const start=new Date();await page.clock.install({time:start});await page.clock.pauseAt(start);let calls=0;
 await page.route('**/api/v1/fleet?**',r=>{calls++;return r.fulfill({status:503,headers:{'Retry-After':'1'},json:{code:'busy',message:'Pedidos em curso'}})});
 await page.goto('/');await page.getByRole('button',{name:'Frota',exact:true}).click();await page.getByRole('button',{name:'Veículos',exact:true}).click();
 await expect.poll(()=>calls).toBe(1);
 for(const delay of [1600,2600,4600,8600]){const before=calls;await page.clock.fastForward(delay);await expect.poll(async()=>{await page.clock.fastForward(100);return calls}).toBe(before+1)}
 // Receiving the fifth HTTP response can precede React Query's scheduled notification; flush the paused clock while waiting for the actual UI state.
 await expect.poll(async()=>{await page.clock.fastForward(1);return page.locator('.page-panel').textContent()}).toContain('Frota indisponível devido a erro.');await page.clock.fastForward(10000);expect(calls).toBe(5); // The separate 30-second polling cycle can start a new bounded read.
});

for(const width of [1280,390])test(`popups close by button, outside click and Escape at width ${width}`,async({page})=>{
 await fixture(page);await page.setViewportSize({width,height:800});await page.goto('/');
 if(width<760)await page.getByRole('button',{name:'Abrir operadores'}).click();
 for(const [open,label,close] of [
  ['Fontes e disponibilidade','Fontes e disponibilidade','Fechar fontes'],
  ['Conta e chaves API','Conta e chaves API','Fechar conta'],
  ['Velocidade amostral','Gráfico expandido','Fechar gráfico'],
 ] as const){
  const launch=()=>page.getByRole('button',{name:open,exact:true}).click();
  await launch();const dialog=page.getByRole('dialog',{name:label});await expect(dialog).toBeVisible();
  await dialog.getByRole('heading').first().click();await expect(dialog).toBeVisible();
  await page.getByRole('button',{name:close,exact:true}).click();await expect(dialog).toHaveCount(0);
  await launch();await page.locator('.modal-shade').click({position:{x:5,y:5}});await expect(dialog).toHaveCount(0);
  await launch();await page.keyboard.press('Escape');await expect(dialog).toHaveCount(0);
 }
});

for(const width of [1280,390])test(`search and detail popups dismiss without losing operator selection at width ${width}`,async({page})=>{
 await fixture(page);
 await page.route('**/api/v1/stops?**',r=>r.fulfill({json:{data:[{id:'metro:roma',source_id:'roma',operator_id:'metro',name:'Roma',lat:38.75,lon:-9.14,route_ids:[]}],page:{limit:500,offset:0,total:1,has_more:false,revision:'test'}}}));
 await page.setViewportSize({width,height:800});await page.goto('/');
 if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();
 const input=page.getByLabel('Pesquisar carreira ou paragem');
 await input.fill('Roma');await expect(page.locator('.search-results button').first()).toBeVisible();
 await input.click();await expect(page.locator('.search-popup')).toBeVisible();
 await page.getByRole('button',{name:'Fechar resultados da pesquisa'}).click();await expect(page.locator('.search-popup')).toHaveCount(0);await expect(input).toHaveValue('');
 await input.fill('Roma');await expect(page.locator('.search-results button').first()).toBeVisible();await page.locator('.map canvas').click({position:{x:width-10,y:400}});await expect(page.locator('.search-popup')).toHaveCount(0);
 if(width<760){await expect(page.locator('.sidebar')).not.toHaveClass(/visible/);await page.getByRole('button',{name:'Abrir pesquisa'}).click()}
 await input.fill('Roma');await expect(page.locator('.search-results button').first()).toBeVisible();await page.keyboard.press('Escape');await expect(page.locator('.search-popup')).toHaveCount(0);
 await input.fill('Roma');await page.locator('.search-results button').first().click();const detail=page.locator('.detail-panel');await expect(detail).toContainText('Roma');
 await detail.getByRole('heading').first().click();await expect(detail).toBeVisible();await page.getByRole('button',{name:'Fechar detalhes'}).click();await expect(detail).toHaveCount(0);
 if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await input.fill('Roma');await page.locator('.search-results button').first().click();await expect(detail).toBeVisible();
 await page.locator('.map canvas').click({position:{x:width-10,y:250}});await expect(detail).toHaveCount(0);
 if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await input.fill('Roma');await page.locator('.search-results button').first().click();await expect(detail).toBeVisible();await page.keyboard.press('Escape');await expect(detail).toHaveCount(0);
 await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true');
});

for(const width of [1280,390])test(`map layers close without resetting toggles at width ${width}`,async({page})=>{
 await fixture(page);await page.setViewportSize({width,height:800});await page.goto('/');const launch=page.getByRole('button',{name:'Camadas do mapa'}),panel=page.locator('.layer-panel');
 await launch.click();await page.getByRole('checkbox',{name:'Linhas de comboio'}).uncheck();await expect(panel).toBeVisible();
 await page.getByRole('button',{name:'Fechar camadas'}).click();await expect(panel).toHaveCount(0);await expect(launch).toBeFocused();
 await launch.click();await expect(page.getByRole('checkbox',{name:'Linhas de comboio'})).not.toBeChecked();await panel.getByRole('heading').click();await expect(panel).toBeVisible();
 await page.locator('.map canvas').click({position:{x:width-10,y:250}});await expect(panel).toHaveCount(0);
 await launch.click();await page.keyboard.press('Escape');await expect(panel).toHaveCount(0);await expect(launch).toBeFocused();
 await launch.click();await expect(page.getByRole('checkbox',{name:'Linhas de comboio'})).not.toBeChecked();await expect(page.getByRole('checkbox',{name:'Linhas de metro'})).toBeChecked();
});

for(const width of [1280,390])test(`closing route details preserves an explicit removable filter at width ${width}`,async({page})=>{
 await fixture(page);await page.setViewportSize({width,height:800});
 await page.route('**/api/v1/routes?**',r=>r.fulfill({json:{data:[{id:'metro:1',operator_id:'metro',short_name:'Azul',long_name:'Linha Azul',color:'#00a',stop_ids:[]}],page:{limit:8,offset:0,total:1,has_more:false,revision:'test'}}}));
 await page.route('**/api/v1/routes/*',r=>r.fulfill({json:{id:'metro:1',operator_id:'metro',short_name:'Azul',long_name:'Linha Azul',color:'#00a',stops:[]}}));
 await page.goto('/');if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await page.getByLabel('Pesquisar carreira ou paragem').fill('Azul');await page.locator('.search-results button').first().click();
 const detail=page.locator('.detail-panel');await expect(detail).toBeVisible();await page.locator('.map canvas').click({position:{x:width-10,y:250}});await expect(detail).toHaveCount(0);
 if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await expect(page.locator('.route-filter')).toContainText('Azul');await page.getByRole('button',{name:'Abrir detalhes da carreira selecionada'}).click();await expect(detail).toBeVisible();await detail.getByRole('heading').click();if(width<760)await expect(page.locator('.sidebar')).not.toHaveClass(/visible/);
 await page.getByRole('button',{name:'Fechar detalhes'}).click();await expect(detail).toHaveCount(0);await expect(page.locator('.route-filter')).toContainText('Azul');
 if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await page.getByRole('button',{name:'Abrir detalhes da carreira selecionada'}).click();await page.keyboard.press('Escape');await expect(detail).toHaveCount(0);await expect(page.locator('.route-filter')).toContainText('Azul');
 if(width<760)await page.getByRole('button',{name:'Abrir pesquisa'}).click();await page.getByRole('button',{name:'Remover filtro de carreira'}).click();await expect(page.locator('.route-filter')).toHaveCount(0);await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true');
});

for(const width of [1280,390])test(`historical ranked route opens live detail at width ${width}`,async({page})=>{
 await fixture(page);await page.setViewportSize({width,height:800});
 await page.route('**/api/v1/rankings?**',r=>r.fulfill({json:{data:[{id:'metro:1',route_id:'metro:1',route_name:'Azul',operator_id:'metro',reported_vehicles:0,estimated_vehicles:2}],page:{limit:500,offset:0,total:1,has_more:false,revision:'test'}}}));
 await page.route('**/api/v1/routes/*',r=>r.fulfill({json:{id:'metro:1',operator_id:'metro',short_name:'Azul',long_name:'Linha Azul',color:'#00a',stops:[]}}));
 if(width<760)await page.route('**/api/v1/stops?**',r=>r.fulfill({json:{data:[{id:'metro:roma',source_id:'roma',operator_id:'metro',name:'Roma',lat:38.75,lon:-9.14,route_ids:[]}],page:{limit:500,offset:0,total:1,has_more:false,revision:'test'}}}));
 await page.goto('/');
 if(width<760){await page.getByRole('button',{name:'Abrir pesquisa'}).click();await page.getByLabel('Pesquisar carreira ou paragem').fill('Roma');await page.locator('.search-results button').first().click();await expect(page.locator('.detail-panel')).toContainText('Roma');const history=page.locator('.compact-nav').getByRole('button',{name:'Histórico',exact:true});await history.focus();await page.keyboard.press('Enter')}
 else await page.getByRole('button',{name:'Histórico',exact:true}).click();
 await page.getByRole('button',{name:'Viagens detetadas',exact:true}).click();await page.getByRole('button',{name:'Azul',exact:true}).click();
 await expect(page.locator('.detail-panel')).toContainText('Linha Azul');await expect(page.locator('.detail-panel')).not.toContainText('Roma');await page.locator('.detail-panel').getByRole('heading').click();if(width<760)await expect(page.locator('.sidebar')).not.toHaveClass(/visible/);
 await expect(page.getByRole('button',{name:'Metro de Lisboa',exact:true})).toHaveAttribute('aria-pressed','true');
});

for(const width of [1440,390])test(`traffic pointer controls stay fixed across loading at ${width}px`,async({page})=>{
 await page.setViewportSize({width,height:900});await fixture(page);
 let release:()=>void=()=>{};const gate=new Promise<void>(resolve=>release=resolve);
 await page.route('**/api/v1/traffic?**',async r=>{await gate;await r.fulfill({json:{data:[],page:{limit:500,offset:0,total:0,has_more:false,revision:'test'}}})});
 await page.goto('/');await page.locator(width<760?'.compact-nav':'.tab-icons').getByRole('button',{name:'Trânsito',exact:true}).click();
 const panel=page.locator('.traffic-panel'),weekdays=page.getByLabel('Excluir fins de semana'),terminals=page.getByLabel('Mostrar terminais');
 await expect(panel).toHaveAttribute('aria-busy','true');const before=await weekdays.boundingBox();release();await expect(panel).toHaveAttribute('aria-busy','false');
 const after=await weekdays.boundingBox();expect(after!.y).toBe(before!.y);
 await weekdays.check();await expect(weekdays).toBeChecked();await terminals.check();await expect(terminals).toBeChecked();
 await expect(panel.getByText('Identificação de terminais indisponível nestes feeds.')).toBeVisible();
});
