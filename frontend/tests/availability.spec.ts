import {test,expect,type Page} from '@playwright/test';
async function fixture(page:Page){
 const now=new Date().toISOString();
 const operators=[{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#ec493a',reported_positions:0,estimated_positions:2},{id:'carris',name:'Carris',mode:'bus',color:'#f5b800',reported_positions:1,estimated_positions:0},{id:'cp',name:'CP',mode:'train',color:'#278044',reported_positions:0,estimated_positions:0}].map(o=>({...o,status:'ok',static_status:'ok',live_updated_at:now}));
 const rows=[{operator_id:'metro',vehicles:2,reported_vehicles:0,estimated_vehicles:2,speed_samples:0,model_vehicles:0,plate_vehicles:0,typology_vehicles:0},{operator_id:'carris',vehicles:1,reported_vehicles:1,estimated_vehicles:0,speed_samples:3,model_vehicles:1,plate_vehicles:1,typology_vehicles:1},{operator_id:'cp',vehicles:1,reported_vehicles:1,estimated_vehicles:0,speed_samples:0,model_vehicles:0,plate_vehicles:0,typology_vehicles:0}];
 const paged=(data:unknown[])=>({data,page:{limit:500,offset:0,total:data.length,has_more:false,revision:'test'}});
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',async r=>{
  const u=new URL(r.request().url()),path=u.pathname;let data:unknown=paged([]);
  if(path.endsWith('/config'))data={dev_auth:false,history_retention_days:30,history_resolution_seconds:300,history_collection_status:'collecting',live_refresh_seconds:5};
  if(path.endsWith('/health'))data={status:'ok'};
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
