import type {Page} from '@playwright/test';

// Bridge legacy vehicle-focused fixtures into synthetic complete Metro frames.
// Transport, journey and fallback behavior is tested separately in metro-live.spec.ts.
export async function metroVehicleFixture(page:Page){
 await page.addInitScript(()=>{
  class Stream extends EventTarget {
   closed=false;cursor=0;onerror:(()=>void)|null=null;timer?:ReturnType<typeof setTimeout>;controller?:AbortController;
   constructor(public url:string){super();void this.publish()}
   close(){this.closed=true;clearTimeout(this.timer);this.controller?.abort()}
   async publish(){
    if(this.closed)return;this.controller=new AbortController();
    try{
     const response=await fetch('/api/v1/vehicles?operators=metro',{signal:this.controller.signal});
     if(!response.ok)throw new Error('Synthetic feed unavailable');
     const data=await response.json();if(this.closed)return;
     const vehicles=(data.data??[]).filter((v:{operator_id:string})=>v.operator_id==='metro');
     const frame={revision:String(++this.cursor),published_at:new Date().toISOString(),plan_id:'plan',status:{status:'unconfigured',lines:[],message:'Synthetic direct source unavailable'},history_status:'unavailable',vehicles,trains:[],directions:[],selected_journey_id:null,unassociated_forecasts:[]};
     this.dispatchEvent(new MessageEvent(this.cursor===1?'reset':'frame',{lastEventId:String(this.cursor),data:JSON.stringify(frame)}));
    }catch{if(!this.closed)this.onerror?.()}
    if(!this.closed)this.timer=setTimeout(()=>void this.publish(),5000);
   }
  }
  Object.defineProperty(window,'EventSource',{value:Stream});
 });
}
