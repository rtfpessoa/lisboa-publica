import type {MetroLiveFrame,MetroTrain,Stop,Vehicle} from './api';
import {canonicalStop} from './stationIdentity';

// Estimated operational direction is explicitly distinct from physical confirmation.
export function metroCurrentDirection(train:MetroTrain|undefined,now:number):boolean{
 return !!train&&train.association==='supported'&&Date.parse(train.valid_until)>now&&['estimated','confirmed'].includes(train.direction_evidence?.state??'');
}

// Reject conflicting indices instead of presenting two positions for one visit.
export function metroProgressConsistent(train:MetroTrain|undefined):boolean{
 if(!train)return false;
 const current=train.current_index,next=train.next_index;
 return (current==null||Number.isInteger(current)&&current>=0)&&(next==null||Number.isInteger(next)&&next>=0)&&(current==null||next==null||next>current);
}

// Resolve full-path visit indices by stop_sequence, even when a frame slices calls.
export function metroStationHighlights(train:MetroTrain|undefined,frame:MetroLiveFrame|undefined,stops:Stop[],planId:string|null|undefined,now:number){
 if(!frame||!metroCurrentDirection(train,now)||!metroProgressConsistent(train)||!frame.plan_id||frame.plan_id!==planId)return {};
 const resolve=(index:number|null|undefined)=>{
  if(index==null)return undefined;
  const calls=train!.calls.filter(c=>c.stop_sequence===index&&c.stop_plan_id===frame.plan_id);
  if(calls.length!==1)return undefined;
  const stop=stops.find(s=>s.id===calls[0].stop_id&&s.operator_id==='metro');
  return stop?canonicalStop(stop,stops):undefined;
 };
 const current=resolve(train!.current_index),next=resolve(train!.next_index);
 return {current,next:next?.id===current?.id?undefined:next};
}

// The same marker must be eligible for both the camera and its follow control.
export function metroFollowMarker(frame:MetroLiveFrame|undefined,train:MetroTrain|undefined,vehicles:Vehicle[],selectedId:string|undefined,now:number):Vehicle|undefined{
 if(!frame||!selectedId||!metroCurrentDirection(train,now)||!metroProgressConsistent(train)||train!.vehicle_id!==selectedId||train!.lifecycle&&train!.lifecycle.state!=='active')return;
 if(frame.trains.filter(t=>t.vehicle_id===selectedId&&metroCurrentDirection(t,now)).length!==1)return;
 const matches=vehicles.filter(v=>v.id===selectedId&&v.operator_id==='metro'&&!v.stale&&!v.last_known&&Number.isFinite(v.lat)&&Number.isFinite(v.lon));
 return matches.length===1?matches[0]:undefined;
}
