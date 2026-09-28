import {createContext,useCallback,useEffect,useRef,useState} from 'react';
import type {MetroLiveFrame,Stop,Vehicle} from './api';

export type MetroDelivery={frame?:MetroLiveFrame;error?:string;healthy:boolean;stops:Stop[];stopsPlanId?:string|null;onVehicle?:(v:Vehicle)=>void;onStop?:(s:Stop)=>void;followPaused:boolean;resumeFollow:()=>void;beforeFrame?:(listener:()=>void)=>()=>void};
export const MetroLiveContext=createContext<MetroDelivery>({healthy:false,stops:[],followPaused:false,resumeFollow:()=>{}});
type Interest={selectionEpoch?:number;enabled:boolean;routeId?:string;vehicleId?:string;stopId?:string};

// One owner for map and popup: each accepted complete frame replaces both atomically.
export function useMetroLive(interest:Interest){
 const [frame,setFrame]=useState<MetroLiveFrame>(),[error,setError]=useState<string>(),[healthy,setHealthy]=useState(false);
 const readers=useRef(new Set<()=>void>());
 const beforeFrame=useCallback((listener:()=>void)=>{readers.current.add(listener);return()=>{readers.current.delete(listener)}},[]);
 const pinned=useRef<string|undefined>(undefined),key=JSON.stringify(interest),generation=useRef(0);
 useEffect(()=>{
  const epoch=++generation.current;pinned.current=undefined;setFrame(undefined);setHealthy(false);setError(undefined);
  if(!interest.enabled)return;
  let source:EventSource|undefined,fallbackTimer:ReturnType<typeof setTimeout>|undefined,reconnectTimer:ReturnType<typeof setTimeout>|undefined,resetTimer:ReturnType<typeof setTimeout>|undefined,controller:AbortController|undefined;
  let active=true,streamReady=false,lastCursor=0,retryDelay=1000,notBefore=0,etag='',accepted:MetroLiveFrame|undefined;
  const current=()=>active&&epoch===generation.current&&!document.hidden;
  const params=()=>{const q=new URLSearchParams();if(interest.routeId)q.set('route_id',interest.routeId);if(interest.vehicleId)q.set('vehicle_id',interest.vehicleId);if(interest.stopId)q.set('stop_id',interest.stopId);if(pinned.current)q.set('journey_id',pinned.current);return q.toString()};
  const apply=(next:MetroLiveFrame)=>{
   if(!current())return;
   if(!next.revision||!Array.isArray(next.trains)||!Array.isArray(next.vehicles)||!Array.isArray(next.directions)||!Array.isArray(next.unassociated_forecasts))throw new Error('Quadro Metro inválido.');
   if(pinned.current&&next.selected_journey_id!==pinned.current)throw new Error('Viagem selecionada sem confirmação.');
   if(interest.vehicleId&&next.selected_journey_id&&!pinned.current)pinned.current=next.selected_journey_id;
   for(const listener of readers.current)listener();
   accepted=next;etag=`"${next.revision}"`;setFrame(previous=>previous?.revision===next.revision?previous:next);setError(undefined);
  };
  const delayFrom=(response:Response)=>{const raw=response.headers.get('Retry-After');if(!raw)return 5000;const seconds=Number(raw);return Number.isFinite(seconds)?Math.max(5000,seconds*1000):Math.max(5000,Date.parse(raw)-Date.now())};
  async function fallback(){
   if(!current()||streamReady||controller)return;
   if(Date.now()<notBefore){fallbackTimer=setTimeout(fallback,notBefore-Date.now());return}
   // Reconnect failures share the same snapshot budget, including successful/304 reads.
   notBefore=Date.now()+5000;
   const request=new AbortController();controller=request;const requestTimer=setTimeout(()=>request.abort(),10000);
   try{
    const response=await fetch(`/api/v1/metro/live?${params()}`,{credentials:'same-origin',signal:request.signal,headers:accepted?{'If-None-Match':etag}:{}});
    if(!current()||streamReady||request.signal.aborted)return;
    if(response.status===304){if(!accepted)throw new Error('Quadro Metro em falta.');return}
    if(!response.ok){notBefore=Math.max(notBefore,Date.now()+delayFrom(response));throw new Error('Metro temporariamente indisponível.');}
    apply(await response.json() as MetroLiveFrame);
   }catch(cause){if(current()&&!streamReady&&!request.signal.aborted)setError(cause instanceof Error?cause.message:'Atualização Metro indisponível.')}
   finally{clearTimeout(requestTimer);if(controller===request)controller=undefined;if(current()&&!streamReady)fallbackTimer=setTimeout(fallback,Math.max(5000,notBefore-Date.now()))}
  }
  function connect(){
   if(!current())return;
   lastCursor=0;const stream=new EventSource(`/api/v1/metro/live/stream?${params()}`,{withCredentials:true});source=stream;
   const receive=(event:MessageEvent)=>{
    if(!current()||source!==stream)return;
    const cursor=Number(event.lastEventId);if(!Number.isSafeInteger(cursor)||cursor<=lastCursor)return;
    try{
     const next=JSON.parse(event.data) as MetroLiveFrame;
     // This socket was opened before the first journey was pinned. Reconnect once with that identity.
     const wasPinned=!!pinned.current;apply(next);lastCursor=cursor;
     clearTimeout(resetTimer);streamReady=true;setHealthy(true);retryDelay=1000;clearTimeout(fallbackTimer);controller?.abort();controller=undefined;
     if(!wasPinned&&pinned.current){stream.close();source=undefined;connect()}
    }catch{stream.close();failed(stream)}
   };
   const failed=(stream:EventSource)=>{
    if(!current()||source!==stream)return;clearTimeout(resetTimer);stream.close();source=undefined;streamReady=false;setHealthy(false);
    clearTimeout(fallbackTimer);void fallback();clearTimeout(reconnectTimer);
    reconnectTimer=setTimeout(connect,Math.max(retryDelay,notBefore-Date.now()));retryDelay=Math.min(30000,retryDelay*2);
   };
   resetTimer=setTimeout(()=>failed(stream),10000);
   stream.addEventListener('reset',receive);stream.addEventListener('frame',receive);stream.addEventListener('unavailable',()=>failed(stream));stream.onerror=()=>failed(stream);
  }
  const visibility=()=>{
   source?.close();source=undefined;controller?.abort();controller=undefined;clearTimeout(fallbackTimer);clearTimeout(reconnectTimer);clearTimeout(resetTimer);streamReady=false;setHealthy(false);
   if(!document.hidden){connect();void fallback()}
  };
  connect(); // Fallback starts on a stream failure; healthy streams never duplicate snapshot reads.
  document.addEventListener('visibilitychange',visibility);
  return()=>{active=false;source?.close();controller?.abort();clearTimeout(fallbackTimer);clearTimeout(reconnectTimer);clearTimeout(resetTimer);document.removeEventListener('visibilitychange',visibility)};
 },[key]);
 return {frame,error,healthy,beforeFrame};
}
