import {test,expect,type Page} from '@playwright/test';
import type {MetroPatterns,MetroPatternForecast} from '../src/api';

async function fixture(page:Page,own=true,paused=false,transform?:(data:MetroPatterns)=>void){
 const epoch=Date.now();const iso=(seconds:number)=>new Date(epoch+seconds*1000).toISOString();
 const f:MetroPatternForecast={id:'fixture-waiting',result:'pending',episode:'aaaaaaaaaaaaaaaaaaaaaaaa',issued_at:iso(0),route:'metro:r',direction:'60',stop:'E',stop_name:'Alameda',destination_name:'Aeroporto',platform:'1',train:'fixture-id',function:'waiting',mode:'official+sequential-proxy-components',profile:'fixture-profile',condition:'reported_normal',source_at:iso(-10),official_at:iso(420),own_at:own?iso(360):null,lower_at:null,upper_at:null,unavailable:own?'':'insufficient_compatible_components',components:own?[{origin:'D',target:'E',origin_at:iso(120),seconds:240,samples:2,days:1,historical_fallback:false,general_context:false,oldest_date:"2026-09-27",newest_date:"2026-09-27"}]:[],calibration_samples:1,selected:false,evaluated:false,reference_lower:null,reference_upper:null,error_lower:null,error_upper:null};
 const data:MetroPatterns={operator:'metro',operators:[],evaluation:[],current_day_type:"weekday",calendar:"fixture-calendar",status:paused?'paused':'collecting',message:'Estimativas experimentais; sem validação física.',experimental:true,physical_validation:false,as_of:iso(0),profile:'fixture-profile',storage_bytes:20000,limit_bytes:10000000000,sample_seconds:30,bin_seconds:30,detail_days:7,aggregate_months:12,collected_days:1,gaps:paused?3:0,pending:1,evaluated:0,lost_reference:0,dwell_seconds:null,speed_kmh:null,patterns:[{profile:'fixture-profile',offset_seconds:3600,resolution_seconds:30,condition:'reported_normal',hour:10,direction:'60',route:'metro:r',platform:'1',day_type:'weekday',signals:2,days:1,probability:null,mean_headway_seconds:null,mean_component_seconds:null,component_target:'',component_samples:0}],forecasts:[f]};
 transform?.(data);
 await page.route('https://tiles.openfreemap.org/styles/positron',r=>r.fulfill({json:{version:8,sources:{},layers:[{id:'background',type:'background',paint:{'background-color':'#fff'}}]}}));
 await page.route('**/api/v1/**',r=>{
  const url=new URL(r.request().url());const path=url.pathname;let response:unknown={data:[],page:{limit:500,offset:0,total:0,has_more:false,revision:'fixture'}};
  if(path.endsWith('/config'))response={dev_auth:false,live_refresh_seconds:30,history_retention_days:30,history_resolution_seconds:300};
  if(path.endsWith('/operators'))response={data:[{id:'metro',name:'Metro de Lisboa',mode:'metro',color:'#ec493a',status:'ok',static_status:'ok',reported_positions:0,estimated_positions:0,note:''}],page:{limit:500,offset:0,total:1,has_more:false,revision:'fixture'}};
  if(path.endsWith('/stops'))response={data:[{id:'metro:E',source_id:'E',operator_id:'metro',name:'Alameda',lat:38.72,lon:-9.13,route_ids:['metro:r']}],page:{limit:500,offset:0,total:1,has_more:false,revision:'fixture'}};
  if(path.endsWith('/route-shapes'))response={data:[],coverage:[],page:{limit:500,offset:0,total:0,has_more:false,revision:'fixture'}};
  if(path.endsWith('/metrics'))response={speed_kmh:null};
  if(path.endsWith('/metro/status'))response={status:'ok',message:'Fonte de teste',lines:[],source_url:'https://example.test'};
  if(path.endsWith('/metro/patterns'))response=url.searchParams.has('episode')?{...data,forecasts:[f,{...f,id:'fixture-onward',function:'onward',stop:'F',stop_name:'Aeroporto'}]}:data;
  return r.fulfill({json:response});
 });
 return {official:new Date(f.official_at!).toLocaleTimeString('pt-PT',{timeZone:'Europe/Lisbon',hour:'2-digit',minute:'2-digit'}),own:f.own_at?new Date(f.own_at).toLocaleTimeString('pt-PT',{timeZone:'Europe/Lisbon',hour:'2-digit',minute:'2-digit'}):null};
}

test('patterns show sparse support, both forecasts and following stations',async({page})=>{
 const values=await fixture(page);await page.goto('/');await page.locator('.sidebar nav').getByRole('button',{name:'Padrões',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Padrões e previsões do Metro'})).toBeVisible();
 await expect(page.getByText('Poucos dados: os valores disponíveis podem ser pouco precisos.')).toBeVisible();
 await expect(page.locator('.transport-patterns aside')).toContainText(values.official);
 await expect(page.locator('.transport-patterns aside')).toContainText(values.own!);
 await expect(page.getByRole('heading',{name:'Chegadas seguintes da mesma associação'})).toBeVisible();
 await expect(page.locator('.transport-patterns aside table')).toHaveCount(2);
 await expect(page.locator('.transport-patterns')).toContainText('falta um denominador');
 await expect(page.locator('.transport-patterns')).toContainText('Faixa indisponível (1 scores)');
});

test('own unavailability and archive pause do not hide official forecast',async({page})=>{
 const values=await fixture(page,false,true);await page.goto('/');await page.locator('.sidebar nav').getByRole('button',{name:'Padrões',exact:true}).click();
 await expect(page.locator('.transport-patterns')).toContainText('Estado da recolha: paused');
 await expect(page.locator('.transport-patterns aside')).toContainText(values.official);
 await expect(page.locator('.transport-patterns aside')).toContainText('Ainda sem histórico compatível');
});

test('mobile patterns fit and remain usable',async({page})=>{
 await page.setViewportSize({width:390,height:844});await fixture(page);await page.goto('/');await page.locator('.compact-nav').getByRole('button',{name:'Padrões',exact:true}).click();
 await expect(page.getByLabel('Estação dos padrões')).toBeVisible();
 await expect.poll(()=>page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth)).toBe(true);
});

// Synthetic admission failures exercise the same shared retry policy as production.
test('patterns recover promptly from transient admission failures for both reads',async({page})=>{
 await fixture(page);
 const attempts={station:0,journey:0};
 await page.route('**/api/v1/metro/patterns**',async route=>{
  const key=new URL(route.request().url()).searchParams.has('episode')?'journey':'station';
  attempts[key]++;
  if(attempts[key]<=2)return route.fulfill({status:503,headers:{'Retry-After':'1'},json:{code:'busy',message:'Synthetic admission contention'}});
  return route.fallback();
 });
 await page.goto('/');await page.locator('.sidebar nav').getByRole('button',{name:'Padrões',exact:true}).click();
 await expect(page.locator('.transport-patterns aside table')).toHaveCount(2,{timeout:15000});
 expect(attempts.station).toBe(3);expect(attempts.journey).toBe(3);
});


test('durable report discloses bounded proxy metrics and older context',async({page})=>{
 await fixture(page,true,false,data=>{
  data.forecasts[0].components[0].historical_fallback=true;
  data.forecasts[0].components[0].general_context=true;
  data.evaluation=[{cohort:'paired',support:'proxy_same_source',function:'waiting',direction:'60',route:'metro:r',mode:'fixture',profile:'fixture-s30',condition:'reported_normal',horizon:0,cases:10,official_available:10,own_available:8,paired:8,evaluated:6,journeys:2,days:2,mae_own_lower:20,mae_own_upper:30,mae_official_lower:40,mae_official_upper:50,p90_own_lower:30,p90_own_upper:60,p90_official_lower:60,p90_official_upper:90,band_cases:4,band_certain:2,band_possible:3,journey_count_complete:false}];
  data.operators=[{operator:'metro',enabled:true,status:'experimental',forecasts:true,physical_validation:false,as_of:data.as_of,samples:0},{operator:'cm',enabled:true,status:'experimental',forecasts:false,physical_validation:false,as_of:data.as_of,samples:3}];
 });
 await page.goto('/');await page.locator('.sidebar nav').getByRole('button',{name:'Padrões',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Avaliação das duas previsões'})).toBeVisible();
 await expect(page.locator('.transport-patterns')).toContainText('6 avaliadas / 10 emitidas');
 await expect(page.locator('.transport-patterns')).toContainText('50–75% (4 casos)');
 await expect(page.locator('.transport-patterns aside')).toContainText('Base histórica antiga');
 await expect(page.locator('.transport-patterns aside')).toContainText('Base geral');
 await page.getByText('Recolha por operador',{exact:true}).click();
 await expect(page.locator('.transport-patterns')).toContainText('CM: recolha experimental · previsões próprias ainda indisponíveis');
});

test('holiday grouping is selected from the current Lisbon civil day',async({page})=>{
 await fixture(page,true,false,data=>{data.current_day_type='holiday';data.patterns[0].day_type='holiday'});
 await page.goto('/');await page.locator('.sidebar nav').getByRole('button',{name:'Padrões',exact:true}).click();
 await expect(page.getByRole('combobox').filter({has:page.locator('option[value="holiday"]')})).toHaveValue('holiday');
 await expect(page.locator('.hour-has-data')).toContainText('10h');
 await expect(page.locator('.hour-has-data')).toContainText('2');
});

test('operator stages expose both points and onward calls without mixing stop identities',async({page})=>{
 await fixture(page);
 const iso=(seconds:number)=>new Date(Date.now()+seconds*1000).toISOString();
 await page.route('**/api/v1/stops**',route=>{
  const op=new URL(route.request().url()).searchParams.get('operators')??'metro';
  return route.fulfill({json:{data:[{id:op+':A',source_id:'A',operator_id:op,name:'Paragem '+op,lat:38.72,lon:-9.13,route_ids:[op+':r']}],page:{limit:500,offset:0,total:1,has_more:false,revision:'fixture-'+op}}});
 });
 const requests:string[]=[];
 await page.route('**/api/v1/transport/patterns**',route=>{
  const url=new URL(route.request().url());const op=url.searchParams.get('operator_id')!;requests.push(url.toString());
  const f:MetroPatternForecast={id:op+'-waiting',result:'pending',episode:'bbbbbbbbbbbbbbbbbbbbbbbb',issued_at:iso(0),route:op+':r',direction:'0',stop:op+':A',stop_name:'Paragem '+op,destination_name:'Destino publicado',platform:'visit:4',train:op+':published',function:'waiting',mode:'official+published-stop-components/v1',profile:'fixture',condition:'unknown',source_at:iso(-5),official_at:iso(420),own_at:iso(360),lower_at:null,upper_at:null,unavailable:'',components:[{origin:op+':B',target:op+':A',origin_at:iso(120),seconds:240,samples:2,days:1,historical_fallback:false,general_context:false,oldest_date:'2026-09-28',newest_date:'2026-09-28'}],calibration_samples:0,selected:false,evaluated:false,reference_lower:null,reference_upper:null,error_lower:null,error_upper:null};
  return route.fulfill({json:{operator:op,operators:[],evaluation:[],current_day_type:'weekday',calendar:'synthetic',status:'collecting',message:'Dados sintéticos.',experimental:true,physical_validation:false,as_of:iso(0),profile:'fixture',storage_bytes:1000,limit_bytes:10000000000,sample_seconds:30,bin_seconds:30,detail_days:7,aggregate_months:12,collected_days:1,gaps:0,pending:1,evaluated:0,lost_reference:0,dwell_seconds:null,speed_kmh:null,patterns:[],forecasts:url.searchParams.has('episode')?[f,{...f,id:op+'-onward',stop:op+':C',stop_name:'Alvo seguinte '+op,function:'onward'}]:[f]}});
 });
 await page.goto('/');await page.locator('.sidebar nav').getByRole('button',{name:'Padrões',exact:true}).click();
 for(const op of ['cm','carris','cp','fertagus','ttsl','tcb','mobi']){
  await page.getByLabel('Operador dos padrões').selectOption(op);
  await expect(page.getByLabel('Estação dos padrões')).toHaveValue(op+':A');
  await expect(page.locator('.transport-patterns aside')).toContainText('Alvo seguinte '+op);
  await expect(page.locator('.transport-patterns aside')).toContainText('visita visit:4');
  await expect(page.locator('.transport-patterns')).toContainText('transições de estados publicados');
 }
 expect(requests.some(raw=>{const u=new URL(raw);const stop=u.searchParams.get('stop_id');return stop&&stop.split(':')[0]!==u.searchParams.get('operator_id')})).toBeFalsy();
});


test('unassociated official cache values remain visible beside a supported own journey',async({page})=>{
 await fixture(page,true,false,data=>{const own=data.forecasts[0];own.official_at=null;data.forecasts.push({...own,id:'independent-official',episode:'',direction:'unassociated',train:'published-trip',mode:'official-cache/v1',own_at:null,official_at:new Date(Date.now()+500000).toISOString(),unavailable:'association_not_supported',components:[]})});
 await page.goto('/');await page.locator('.sidebar nav').getByRole('button',{name:'Padrões',exact:true}).click();
 await expect(page.getByRole('heading',{name:'Previsões oficiais sem associação',exact:true})).toBeVisible();
 await expect(page.locator('.transport-patterns aside')).toContainText('não formam uma comparação pareada');
 await expect(page.locator('.transport-patterns aside')).toContainText('published-trip');
});
