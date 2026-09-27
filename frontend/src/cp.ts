import * as api from './api';

// One bounded cached collection, with a shared opaque revision across its pages.
async function cpPages(args:{stopId?:string,routeId?:string},signal?:AbortSignal){
 const now=Date.now();
 const window={$from:new Date(now).toISOString(),to:new Date(now+7200000).toISOString()};
 let first:api.CpPredictionPage|undefined,revision:string|undefined,offset=0;
 const data:api.CpPrediction[]=[];
 while(true){
  const page=await api.listCpPredictions({...args,...window,operators:'cp',limit:500,offset,revision},{signal});
  if(!page.availability||!page.page?.revision||!Array.isArray(page.data))throw new Error('Resposta de previsões CP indisponível.');
  first??=page;
  if(page.page.revision!==first.page.revision||page.page.total!==first.page.total)throw new Error('A coleção de previsões mudou.');
  data.push(...page.data);
  if(data.length>1024)throw new Error('Limite de previsões excedido.');
  if(!page.page.has_more)return {...first,data};
  if(!page.data.length)throw new Error('Página de previsões incompleta.');
  revision=page.page.revision??undefined;offset+=page.page.limit;
 }
}

export async function loadCPPredictions(args:{routeId?:string},signal?:AbortSignal){
 for(let attempt=0;attempt<2;attempt++){
  try{return await cpPages(args,signal)}
  catch(error){if((error as {status?:number}).status!==410||attempt===1)throw error}
 }
 throw new Error('Previsões indisponíveis.');
}

export async function loadCPStation(stopId:string,signal?:AbortSignal){
 for(let attempt=0;attempt<2;attempt++){
  try{
   const predictions=await cpPages({stopId},signal);
   const planned:api.Arrival[]=[];let offset=0;
   while(true){
    const page=await api.listArrivals({operators:'cp',stopId,revision:predictions.page.revision??undefined,limit:500,offset},{signal});
    if(page.page.revision!==predictions.page.revision)throw new Error('A revisão dos horários mudou.');
    planned.push(...page.data);
    if(planned.length>100000)throw new Error('Limite de horários excedido.');
    if(!page.page.has_more)return {predictions,planned};
    if(!page.data.length)throw new Error('Página de horários incompleta.');
    offset+=page.page.limit;
   }
  }catch(error){if((error as {status?:number}).status!==410||attempt===1)throw error}
 }
 throw new Error('Chegadas indisponíveis.');
}

export function currentCPPredictions(rows:api.CpPrediction[],now:number){
 return rows.filter(row=>Date.parse(row.valid_until)>now&&Date.parse(row.expected_at)>=now);
}

export function predictionMatchesArrival(p:api.CpPrediction,a:api.Arrival){
 return p.service_date!=null&&p.scheduled_at!=null&&a.plan_id===p.plan_id&&a.operator_id===p.operator_id&&a.source_trip_id===p.source_trip_id&&a.service_date===p.service_date&&a.stop_id===p.stop_id&&a.stop_sequence===p.stop_sequence&&a.scheduled_at!=null&&Date.parse(a.scheduled_at)===Date.parse(p.scheduled_at);
}

export function vehiclePredictions(rows:api.CpPrediction[],v:api.Vehicle){
 if(v.operator_id!=='cp'||!v.plan_id||!v.trip_id||!v.operational_date)return [];
 return rows.filter(p=>p.plan_id===v.plan_id&&p.source_trip_id===v.trip_id&&p.service_date===v.operational_date).sort((a,b)=>a.stop_sequence-b.stop_sequence);
}
